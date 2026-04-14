package telemt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

func TestNewAPIDefaultControlPortWhenAPIPortUnset(t *testing.T) {
	t.Parallel()

	bridge, err := NewAPI(APIOptions{
		Runtime: baseRuntimeInstallation(runtime.ProviderTelemt, nil),
		Logger:  discardLogger(),
	})
	if err != nil {
		t.Fatalf("expected bridge initialization success, got error: %v", err)
	}

	if bridge.ControlEndpoint() != "http://127.0.0.1:9091" {
		t.Fatalf("unexpected default control endpoint: %q", bridge.ControlEndpoint())
	}
}

func TestNewAPIRejectsUnsupportedProvider(t *testing.T) {
	t.Parallel()

	_, err := NewAPI(APIOptions{
		Runtime: baseRuntimeInstallation(runtime.ProviderMTG, nil),
		Logger:  discardLogger(),
	})
	if err == nil {
		t.Fatalf("expected unsupported provider to be rejected")
	}
}

func TestAPIResolveStartupLinkScenarios(t *testing.T) {
	const usableLink = "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"

	testCases := []struct {
		name       string
		statusCode int
		body       string
		delay      time.Duration
		timeout    time.Duration
		wantErr    telemtapi.RequestErrorKind
		wantClass  telemtapi.UsersParseClass
		wantReason string
	}{
		{
			name:      "legacy map-shaped payload is accepted by bridge",
			body:      `{"main":{"tls":["` + usableLink + `"]}}`,
			wantClass: telemtapi.UsersParseClassUsableLink,
		},
		{
			name: "legacy payload with reserved usernames is accepted by bridge",
			body: `{
				"ok":{"tls":["` + usableLink + `"]},
				"data":{"tls":["` + usableLink + `"]},
				"users":{"tls":["` + usableLink + `"]},
				"revision":{"tls":["` + usableLink + `"]}
			}`,
			wantClass: telemtapi.UsersParseClassUsableLink,
		},
		{
			name: "wrapper data array with nested links tls is accepted by bridge",
			body: `{
				"ok": true,
				"data": [{
					"username": "main",
					"in_runtime": true,
					"links": {
						"classic": [],
						"secure": [],
						"tls": ["` + usableLink + `"]
					}
				}],
				"revision": "test-revision"
			}`,
			wantClass: telemtapi.UsersParseClassUsableLink,
		},
		{
			name:       "wrapper with ok false is degraded",
			body:       `{"ok":false,"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:  telemtapi.UsersParseClassIncompleteStructure,
			wantReason: "response_not_ok",
		},
		{
			name:       "schema drift stays incomplete payload shape",
			body:       `{"ok":true,"users":["` + usableLink + `"]}`,
			wantClass:  telemtapi.UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "recognized user without tls is reported as no tls links",
			body:       `{"ok":true,"users":{"main":{"username":"main"}}}`,
			wantClass:  telemtapi.UsersParseClassNoTLSLinks,
			wantReason: "users_without_tls_links",
		},
		{
			name:       "non-string username is reported as incomplete payload shape",
			body:       `{"ok":true,"users":{"main":{"username":123}}}`,
			wantClass:  telemtapi.UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "top-level array payload is reported as incomplete payload shape",
			body:       `[{"tls":["` + usableLink + `"]}]`,
			wantClass:  telemtapi.UsersParseClassIncompleteStructure,
			wantReason: "payload_not_object",
		},
		{
			name:       "non 200 is propagated as request error",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"error":"temporary"}`,
			wantErr:    telemtapi.RequestErrorKindHTTPStatus,
		},
		{
			name:    "timeout is propagated as transport error",
			body:    `{"ok":true,"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			delay:   220 * time.Millisecond,
			timeout: 40 * time.Millisecond,
			wantErr: telemtapi.RequestErrorKindTransport,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			var logBuffer bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

			statusCode := testCase.statusCode
			if statusCode == 0 {
				statusCode = http.StatusOK
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/users" {
					http.NotFound(w, r)
					return
				}

				if testCase.delay > 0 {
					time.Sleep(testCase.delay)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			bridge := mustNewBridgeForServer(t, server, logger, testCase.timeout)
			result, err := bridge.ResolveStartupLink(context.Background())

			if testCase.wantErr != "" {
				assertRequestErrorKind(t, err, testCase.wantErr)
				return
			}

			if err != nil {
				t.Fatalf("expected startup link resolution success, got error: %v", err)
			}

			if result.Selection.Class != testCase.wantClass {
				t.Fatalf("unexpected class: got %q, want %q", result.Selection.Class, testCase.wantClass)
			}
			if result.Selection.DegradedReason != testCase.wantReason {
				t.Fatalf("unexpected degraded reason: got %q, want %q", result.Selection.DegradedReason, testCase.wantReason)
			}

			logs := logBuffer.String()
			if strings.Contains(logs, usableLink) {
				t.Fatalf("expected startup link to stay redacted in bridge logs, got: %s", logs)
			}
		})
	}
}

func mustNewBridgeForServer(t *testing.T, server *httptest.Server, logger *slog.Logger, timeout time.Duration) *API {
	t.Helper()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse server url: %v", err)
	}

	envFile := mustLoadEnvFile(t, "API_PORT="+parsedURL.Port())

	if timeout <= 0 {
		timeout = 400 * time.Millisecond
	}

	bridge, err := NewAPI(APIOptions{
		Runtime: baseRuntimeInstallation(runtime.ProviderTelemt, envFile),
		Timeout: timeout,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("failed to initialize telemt bridge: %v", err)
	}

	bridge.client, err = telemtapi.NewClient(telemtapi.ClientOptions{
		BaseURL:    bridge.ControlEndpoint(),
		Timeout:    timeout,
		HTTPClient: server.Client(),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("failed to override bridge client for test server: %v", err)
	}

	return bridge
}

func mustLoadEnvFile(t *testing.T, lines ...string) *runtime.EnvFile {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}

	envFile, err := runtime.LoadEnv(path, discardLogger())
	if err != nil {
		t.Fatalf("failed to load env file: %v", err)
	}

	return envFile
}

func baseRuntimeInstallation(provider runtime.Provider, envFile *runtime.EnvFile) *runtime.RuntimeInstallation {
	return &runtime.RuntimeInstallation{
		InstallDir: "/tmp/mtproxy-installer",
		Paths: runtime.RuntimePaths{
			InstallDir: "/tmp/mtproxy-installer",
		},
		Provider: runtime.ProviderDescriptor{
			Name: provider,
		},
		Env: envFile,
	}
}

func assertRequestErrorKind(t *testing.T, err error, wantKind telemtapi.RequestErrorKind) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected request error kind %q, got nil", wantKind)
	}

	var requestErr *telemtapi.RequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("expected *telemtapi.RequestError, got %T", err)
	}
	if requestErr.Kind != wantKind {
		t.Fatalf("unexpected request error kind: got %q, want %q", requestErr.Kind, wantKind)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
