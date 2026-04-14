package telemtapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
			name: "wrapper data array with nested links tls is parsed",
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

func TestNewClientValidationAndDefaults(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		baseURL     string
		wantErrPart string
		forbidden   []string
	}{
		{
			name:        "empty base url is rejected",
			baseURL:     "",
			wantErrPart: "base URL is required",
		},
		{
			name:        "unsupported base url scheme is rejected",
			baseURL:     "ftp://example.com",
			wantErrPart: "scheme is unsupported",
		},
		{
			name:        "base url without host is rejected",
			baseURL:     "https://",
			wantErrPart: "base URL host is required",
		},
		{
			name:        "base url with userinfo is rejected",
			baseURL:     "http://user:pass@127.0.0.1:9091",
			wantErrPart: "must not include userinfo",
			forbidden:   []string{"user:pass"},
		},
		{
			name:        "base url with query is rejected",
			baseURL:     "http://127.0.0.1:9091?token=supersecret",
			wantErrPart: "must not include query parameters",
			forbidden:   []string{"token=supersecret", "supersecret"},
		},
		{
			name:        "malformed base url does not leak raw input",
			baseURL:     "http://127.0.0.1:9091?token=supersecret%zz",
			wantErrPart: "base URL is invalid",
			forbidden:   []string{"token=supersecret", "supersecret", "%zz"},
		},
		{
			name:        "base url with fragment is rejected",
			baseURL:     "http://127.0.0.1:9091#session=abc",
			wantErrPart: "must not include fragment",
			forbidden:   []string{"session=abc"},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewClient(ClientOptions{BaseURL: testCase.baseURL})
			if err == nil {
				t.Fatalf("expected validation failure for base url %q", testCase.baseURL)
			}
			if !strings.Contains(err.Error(), testCase.wantErrPart) {
				t.Fatalf("unexpected validation error: got %v want substring %q", err, testCase.wantErrPart)
			}
			for _, marker := range testCase.forbidden {
				if strings.Contains(err.Error(), marker) {
					t.Fatalf("expected validation error to avoid leaking %q, got %v", marker, err)
				}
			}
		})
	}

	client, err := NewClient(ClientOptions{BaseURL: "http://127.0.0.1:9091/"})
	if err != nil {
		t.Fatalf("expected default client construction success, got %v", err)
	}

	if client.baseURL != "http://127.0.0.1:9091" {
		t.Fatalf("unexpected normalized base url: got %q", client.baseURL)
	}
	if client.timeout != DefaultTimeout {
		t.Fatalf("expected default timeout %s, got %s", DefaultTimeout, client.timeout)
	}
	if client.maxResponseBytes != DefaultMaxResponseBytes {
		t.Fatalf("expected default max response bytes %d, got %d", DefaultMaxResponseBytes, client.maxResponseBytes)
	}
	if client.httpClient == nil || client.httpClient.CheckRedirect == nil {
		t.Fatalf("expected safe redirect policy to be configured")
	}
	if err := client.httpClient.CheckRedirect(&http.Request{}, []*http.Request{}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("expected redirect policy to return http.ErrUseLastResponse, got %v", err)
	}
}

func TestClientGetHealthSuccessAndDegradedLogging(t *testing.T) {
	testCases := []struct {
		name           string
		body           string
		wantClass      HealthParseClass
		wantReason     string
		wantHealthy    bool
		wantLogMessage string
		wantLogLevel   string
	}{
		{
			name:           "healthy payload emits info success log",
			body:           `{"ok":true,"data":{"status":"ok","read_only":true}}`,
			wantClass:      HealthParseClassComplete,
			wantReason:     "",
			wantHealthy:    true,
			wantLogMessage: "telemt api health fetched",
			wantLogLevel:   "level=INFO",
		},
		{
			name:           "degraded payload emits warn log",
			body:           `{"ok":true,"data":{"status":"ok"}}`,
			wantClass:      HealthParseClassIncomplete,
			wantReason:     "missing_read_only",
			wantHealthy:    false,
			wantLogMessage: "telemt api health payload degraded",
			wantLogLevel:   "level=WARN",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/health" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			client, err := NewClient(ClientOptions{
				BaseURL:    server.URL,
				Timeout:    500 * time.Millisecond,
				HTTPClient: server.Client(),
				Logger:     logger,
			})
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			result, err := client.GetHealth(context.Background())
			if err != nil {
				t.Fatalf("expected health call success, got %v", err)
			}
			if result.ParseClass != testCase.wantClass {
				t.Fatalf("unexpected parse class: got %q want %q", result.ParseClass, testCase.wantClass)
			}
			if result.DegradedReason != testCase.wantReason {
				t.Fatalf("unexpected degraded reason: got %q want %q", result.DegradedReason, testCase.wantReason)
			}
			if result.Payload.IsHealthy() != testCase.wantHealthy {
				t.Fatalf("unexpected health status: got %t want %t", result.Payload.IsHealthy(), testCase.wantHealthy)
			}

			logText := logs.String()
			if !strings.Contains(logText, "telemt api request start") {
				t.Fatalf("expected debug request-start log, got: %s", logText)
			}
			if !strings.Contains(logText, "telemt api request finish") {
				t.Fatalf("expected debug request-finish log, got: %s", logText)
			}
			if !strings.Contains(logText, testCase.wantLogMessage) {
				t.Fatalf("expected health log message %q, got: %s", testCase.wantLogMessage, logText)
			}
			if !strings.Contains(logText, testCase.wantLogLevel) {
				t.Fatalf("expected health log level %q, got: %s", testCase.wantLogLevel, logText)
			}
		})
	}
}

func TestClientGetHealthParseFailureIsWarnAndRedacted(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const secretToken = "token-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(
			w,
			fmt.Sprintf(`{"ok":true,"data":{"status":"ok","read_only":true}} {"authToken":"%s"}`, secretToken),
		)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:    server.URL,
		Timeout:    500 * time.Millisecond,
		HTTPClient: server.Client(),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.GetHealth(context.Background())
	assertRequestErrorKind(t, err, RequestErrorKindParse)

	logText := logs.String()
	if !strings.Contains(logText, "telemt api response parsing failed") {
		t.Fatalf("expected parse-failure warning log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Fatalf("expected WARN level for parse failures, got: %s", logText)
	}
	if strings.Contains(logText, secretToken) {
		t.Fatalf("expected parse-failure logs to avoid raw payload values, got: %s", logText)
	}
}

func TestClientGetHealthNon200IsWarn(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"upstream unavailable"}`)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:    server.URL,
		Timeout:    500 * time.Millisecond,
		HTTPClient: server.Client(),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.GetHealth(context.Background())
	assertRequestErrorKind(t, err, RequestErrorKindHTTPStatus)

	logText := logs.String()
	if !strings.Contains(logText, "telemt api request returned non-200 status") {
		t.Fatalf("expected non-200 warning log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Fatalf("expected WARN level for non-200 responses, got: %s", logText)
	}
}

func TestClientGetHealthTimeoutIsTransportErrorWithErrorLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"status":"ok","read_only":true}}`)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:    server.URL,
		Timeout:    40 * time.Millisecond,
		HTTPClient: server.Client(),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.GetHealth(context.Background())
	assertRequestErrorKind(t, err, RequestErrorKindTransport)

	logText := logs.String()
	if !strings.Contains(logText, "telemt api request failed") {
		t.Fatalf("expected transport error log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR level for transport failures, got: %s", logText)
	}
}

func TestClientGetHealthResponseTooLargeIsError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"status":"ok","read_only":true},"padding":"0123456789abcdef"}`)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:          server.URL,
		Timeout:          500 * time.Millisecond,
		HTTPClient:       server.Client(),
		Logger:           logger,
		MaxResponseBytes: 32,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.GetHealth(context.Background())
	assertRequestErrorKind(t, err, RequestErrorKindResponseTooLarge)

	logText := logs.String()
	if !strings.Contains(logText, "telemt api request failed") {
		t.Fatalf("expected response-too-large error log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR level for response-too-large failures, got: %s", logText)
	}
}

func TestClientGetHealthDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var redirectedHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"status":"ok","read_only":true}}`)
	}))
	defer redirectTarget.Close()

	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, redirectTarget.URL+"/v1/health", http.StatusFound)
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

	_, err = client.GetHealth(context.Background())
	assertRequestErrorKind(t, err, RequestErrorKindHTTPStatus)

	var requestErr *RequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("expected request error, got %T", err)
	}
	if requestErr.StatusCode != http.StatusFound {
		t.Fatalf("unexpected status code: got %d want %d", requestErr.StatusCode, http.StatusFound)
	}
	if redirectedHits.Load() != 0 {
		t.Fatalf("expected redirect target to remain uncalled, got %d calls", redirectedHits.Load())
	}
}

func TestReadBoundedBodyRespectsConfiguredLimit(t *testing.T) {
	t.Parallel()

	client := &Client{maxResponseBytes: 4}
	body, err := client.readBoundedBody(strings.NewReader("1234"))
	if err != nil {
		t.Fatalf("expected bounded read success, got %v", err)
	}
	if string(body) != "1234" {
		t.Fatalf("unexpected bounded body: %q", string(body))
	}

	_, err = client.readBoundedBody(strings.NewReader("12345"))
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("expected response-too-large error, got %v", err)
	}
}

func TestDecodeJSONRejectsEmptyAndTrailingPayload(t *testing.T) {
	t.Parallel()

	var payload map[string]any
	if err := decodeJSON([]byte(""), &payload); err == nil {
		t.Fatalf("expected empty payload decode failure")
	}

	if err := decodeJSON([]byte(`{"ok":true} {"next":1}`), &payload); err == nil {
		t.Fatalf("expected trailing JSON payload decode failure")
	}

	if err := decodeJSON([]byte(`{"ok":true}`), &payload); err != nil {
		t.Fatalf("expected valid JSON decode success, got %v", err)
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
