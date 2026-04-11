package telemtapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientGetUsersScenarios(t *testing.T) {
	const usableLink = "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"

	testCases := []struct {
		name       string
		statusCode int
		body       string
		delay      time.Duration
		timeout    time.Duration
		wantErr    RequestErrorKind
		wantClass  UsersParseClass
		wantReason string
	}{
		{
			name:      "legacy map-shaped users response is parsed",
			body:      `{"main":{"tls":["` + usableLink + `"]}}`,
			wantClass: UsersParseClassUsableLink,
		},
		{
			name: "legacy map-shaped users with reserved usernames is parsed",
			body: `{
				"ok":{"tls":["` + usableLink + `"]},
				"data":{"tls":["` + usableLink + `"]},
				"users":{"tls":["` + usableLink + `"]},
				"revision":{"tls":["` + usableLink + `"]}
			}`,
			wantClass: UsersParseClassUsableLink,
		},
		{
			name:       "ok false response is classified as incomplete payload shape",
			body:       `{"ok":false,"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "response_not_ok",
		},
		{
			name:       "non-boolean ok response is classified as incomplete payload shape",
			body:       `{"ok":"true","users":{"main":{"tls":["` + usableLink + `"]}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "response_not_ok",
		},
		{
			name:       "schema drift users payload is incomplete payload shape",
			body:       `{"ok":true,"users":{"main":{"profile":{"tls":["` + usableLink + `"]}}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "top-level array payload is classified as incomplete payload shape",
			body:       `[{"tls":["` + usableLink + `"]}]`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "payload_not_object",
		},
		{
			name:       "recognized user without tls stays degraded with no tls links class",
			body:       `{"ok":true,"users":{"main":{"username":"main"}}}`,
			wantClass:  UsersParseClassNoTLSLinks,
			wantReason: "users_without_tls_links",
		},
		{
			name:       "non-string username is classified as incomplete payload shape",
			body:       `{"ok":true,"users":{"main":{"username":123}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "non-string name is classified as incomplete payload shape",
			body:       `{"ok":true,"users":{"main":{"name":{"first":"main"}}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "non-string user is classified as incomplete payload shape",
			body:       `{"ok":true,"users":{"main":{"user":true}}}`,
			wantClass:  UsersParseClassIncompleteStructure,
			wantReason: "users_collection_missing_or_unsupported",
		},
		{
			name:       "malformed json response is parse error",
			body:       `{"ok":true`,
			wantErr:    RequestErrorKindParse,
			statusCode: http.StatusOK,
		},
		{
			name:       "non 200 response is classified explicitly",
			statusCode: http.StatusBadGateway,
			body:       `{"error":"upstream unavailable"}`,
			wantErr:    RequestErrorKindHTTPStatus,
		},
		{
			name:    "timeout returns transport error",
			body:    `{"ok":true,"users":{"main":{"tls":["` + usableLink + `"]}}}`,
			delay:   200 * time.Millisecond,
			timeout: 40 * time.Millisecond,
			wantErr: RequestErrorKindTransport,
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

			timeout := testCase.timeout
			if timeout <= 0 {
				timeout = 500 * time.Millisecond
			}

			client, err := NewClient(ClientOptions{
				BaseURL:    server.URL,
				Timeout:    timeout,
				HTTPClient: server.Client(),
				Logger:     logger,
			})
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.GetUsers(context.Background())
			if testCase.wantErr != "" {
				assertRequestErrorKind(t, err, testCase.wantErr)
				return
			}

			if err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}

			if result.Selection.Class != testCase.wantClass {
				t.Fatalf("unexpected selection class: got %q, want %q", result.Selection.Class, testCase.wantClass)
			}

			if result.Selection.DegradedReason != testCase.wantReason {
				t.Fatalf("unexpected degraded reason: got %q, want %q", result.Selection.DegradedReason, testCase.wantReason)
			}

			logs := logBuffer.String()
			if strings.Contains(logs, usableLink) {
				t.Fatalf("expected startup link to be redacted in logs, got: %s", logs)
			}
			if result.Selection.HasUsableLink() && !strings.Contains(logs, "[redacted-proxy-link]") {
				t.Fatalf("expected redaction marker in success logs, got: %s", logs)
			}
		})
	}
}

func TestClientGetUsersDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	const usableLink = "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"

	var redirectedHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"main":{"tls":["`+usableLink+`"]}}`)
	}))
	defer redirectTarget.Close()

	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/users" {
			http.NotFound(w, r)
			return
		}

		http.Redirect(w, r, redirectTarget.URL+"/v1/users", http.StatusFound)
	}))
	defer redirectSource.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:    redirectSource.URL,
		Timeout:    500 * time.Millisecond,
		HTTPClient: redirectSource.Client(),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.GetUsers(context.Background())
	assertRequestErrorKind(t, err, RequestErrorKindHTTPStatus)

	var requestErr *RequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("expected request error, got %T", err)
	}
	if requestErr.StatusCode != http.StatusFound {
		t.Fatalf("unexpected status code: got %d, want %d", requestErr.StatusCode, http.StatusFound)
	}
	if redirectedHits.Load() != 0 {
		t.Fatalf("expected redirect target to remain uncalled, got %d calls", redirectedHits.Load())
	}
}

func assertRequestErrorKind(t *testing.T, err error, wantKind RequestErrorKind) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected request error kind %q, got nil", wantKind)
	}

	var requestErr *RequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("expected *RequestError, got %T", err)
	}

	if requestErr.Kind != wantKind {
		t.Fatalf("unexpected request error kind: got %q, want %q", requestErr.Kind, wantKind)
	}
}
