package runtime

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvParsesExportQuotedDuplicateAndRedactsSnapshot(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	content := strings.Join([]string{
		"# comment",
		"export PROVIDER=telemt",
		"PORT=443",
		"PORT=8443",
		"API_PORT=9091",
		`SECRET="supersecret"`,
		`AUTH_TOKEN="runtime-auth-token"`,
		"COOKIE=sessionid=runtime-cookie",
		"SESSION_ID=session-123",
		"STARTUP_LINK=tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
		"CONTROL_URL=http://127.0.0.1:9091?token=runtime-control-token",
		"HTTPS_PROXY=http://user:pass@proxy.internal:3128",
		`MTG_IMAGE='ghcr.io/example/mtg:latest'`,
		"",
	}, "\n")
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write fixture env file: %v", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	envFile, err := LoadEnv(envPath, logger)
	if err != nil {
		t.Fatalf("expected env load success, got %v", err)
	}

	if got := envFile.ProviderValue(); got != "telemt" {
		t.Fatalf("unexpected provider value: got %q, want %q", got, "telemt")
	}

	port, provided, err := envFile.Port()
	if err != nil {
		t.Fatalf("expected valid port, got %v", err)
	}
	if !provided || port != 8443 {
		t.Fatalf("expected overwritten port=8443 to be provided, got provided=%t port=%d", provided, port)
	}

	apiPort, apiProvided, err := envFile.APIPort()
	if err != nil {
		t.Fatalf("expected valid API port, got %v", err)
	}
	if !apiProvided || apiPort != 9091 {
		t.Fatalf("expected API port=9091 to be provided, got provided=%t api_port=%d", apiProvided, apiPort)
	}

	image, ok := envFile.Value("MTG_IMAGE")
	if !ok || image != "ghcr.io/example/mtg:latest" {
		t.Fatalf("unexpected quoted image value: ok=%t value=%q", ok, image)
	}

	snapshot := envFile.SafeSnapshot()
	if got := snapshot["SECRET"]; got != "[redacted]" {
		t.Fatalf("expected SECRET snapshot to be redacted, got %q", got)
	}
	if got := snapshot["AUTH_TOKEN"]; got != "[redacted]" {
		t.Fatalf("expected AUTH_TOKEN snapshot to be redacted, got %q", got)
	}
	if got := snapshot["COOKIE"]; got != "[redacted]" {
		t.Fatalf("expected COOKIE snapshot to be redacted, got %q", got)
	}
	if got := snapshot["SESSION_ID"]; got != "[redacted]" {
		t.Fatalf("expected SESSION_ID snapshot to be redacted, got %q", got)
	}
	if got := snapshot["STARTUP_LINK"]; got != "[redacted-proxy-link]" {
		t.Fatalf("expected STARTUP_LINK snapshot to be redacted, got %q", got)
	}
	if got := snapshot["CONTROL_URL"]; got != "http://127.0.0.1:9091?token=[redacted]" {
		t.Fatalf("expected CONTROL_URL query secret to be redacted, got %q", got)
	}
	if got := snapshot["HTTPS_PROXY"]; got != "http://[redacted]@proxy.internal:3128" {
		t.Fatalf("expected HTTPS_PROXY userinfo to be redacted, got %q", got)
	}
	if got := snapshot["PORT"]; got != "8443" {
		t.Fatalf("expected overwritten PORT in snapshot, got %q", got)
	}

	logText := logs.String()
	if !strings.Contains(logText, "runtime env loaded") {
		t.Fatalf("expected runtime env loaded log, got: %s", logText)
	}
	if !strings.Contains(logText, "env key overwritten") {
		t.Fatalf("expected env overwrite diagnostics, got: %s", logText)
	}
	if strings.Contains(logText, "supersecret") {
		t.Fatalf("expected logs to redact secret values, got: %s", logText)
	}
	if strings.Contains(logText, "runtime-auth-token") {
		t.Fatalf("expected logs to redact auth token values, got: %s", logText)
	}
	if strings.Contains(logText, "sessionid=runtime-cookie") {
		t.Fatalf("expected logs to redact cookie values, got: %s", logText)
	}
	if strings.Contains(logText, "session-123") {
		t.Fatalf("expected logs to redact session values, got: %s", logText)
	}
	if strings.Contains(logText, "tg://proxy?server=127.0.0.1&port=443&secret=abcdef") {
		t.Fatalf("expected logs to redact proxy links, got: %s", logText)
	}
	if strings.Contains(logText, "runtime-control-token") {
		t.Fatalf("expected logs to redact query secrets, got: %s", logText)
	}
	if strings.Contains(logText, "user:pass@proxy.internal") {
		t.Fatalf("expected logs to redact proxy credentials, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted]") {
		t.Fatalf("expected redaction marker in logs, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted-proxy-link]") {
		t.Fatalf("expected proxy-link redaction marker in logs, got: %s", logText)
	}
}

func TestLoadEnvRejectsMalformedKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte("BAD-KEY=value\n"), 0o600); err != nil {
		t.Fatalf("failed to write malformed env fixture: %v", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := LoadEnv(envPath, logger)
	if err == nil {
		t.Fatalf("expected malformed env key error")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeEnvParse {
		t.Fatalf("expected env parse code, got %s", runtimeErr.Code)
	}
	if runtimeErr.Field != "line:1" {
		t.Fatalf("expected field line:1, got %q", runtimeErr.Field)
	}

	logText := logs.String()
	if !strings.Contains(logText, "env parsing failed") {
		t.Fatalf("expected parse failure log, got: %s", logText)
	}
}

func TestParseEnvLineHandlesExportQuotesAndComments(t *testing.T) {
	t.Parallel()

	key, value, skip, err := parseEnvLine("export API_PORT='9091'")
	if err != nil {
		t.Fatalf("expected parse success, got %v", err)
	}
	if skip {
		t.Fatalf("expected parsed env line, got skip=true")
	}
	if key != "API_PORT" || value != "9091" {
		t.Fatalf("unexpected parsed export line: key=%q value=%q", key, value)
	}

	_, _, skip, err = parseEnvLine("# comment")
	if err != nil {
		t.Fatalf("expected comment line parse success, got %v", err)
	}
	if !skip {
		t.Fatalf("expected comment line to be skipped")
	}

	_, _, skip, err = parseEnvLine("AUTH_HEADER Bearer abc123")
	if err == nil {
		t.Fatalf("expected malformed env line to fail")
	}
	if skip {
		t.Fatalf("expected malformed env line to remain actionable, got skip=true")
	}
	if !strings.Contains(err.Error(), "expected KEY=VALUE") {
		t.Fatalf("expected actionable parse message, got %v", err)
	}
	if strings.Contains(err.Error(), "abc123") {
		t.Fatalf("expected parse error to avoid leaking raw values, got %v", err)
	}
}

func TestLoadEnvMalformedLineErrorAndLogsDoNotLeakRawInput(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte("AUTH_HEADER Bearer abc123\n"), 0o600); err != nil {
		t.Fatalf("failed to write malformed env fixture: %v", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := LoadEnv(envPath, logger)
	if err == nil {
		t.Fatalf("expected malformed env line error")
	}

	errText := err.Error()
	logText := logs.String()
	if strings.Contains(errText, "abc123") || strings.Contains(errText, "AUTH_HEADER Bearer abc123") {
		t.Fatalf("expected load error text to avoid raw malformed line leaks, got: %v", err)
	}
	if strings.Contains(logText, "abc123") || strings.Contains(logText, "AUTH_HEADER Bearer abc123") {
		t.Fatalf("expected load logs to avoid raw malformed line leaks, got: %s", logText)
	}
	if !strings.Contains(logText, "env parsing failed") {
		t.Fatalf("expected parse failure log, got: %s", logText)
	}
}

func TestEnvFileIntegerParsingErrorsAreActionable(t *testing.T) {
	t.Parallel()

	envFile := &EnvFile{
		Path: "/tmp/.env",
		values: map[string]string{
			envPortKey:    "not-a-number",
			envAPIPortKey: "9091",
		},
	}

	_, provided, err := envFile.Port()
	if !provided {
		t.Fatalf("expected port key to be reported as provided")
	}
	if err == nil {
		t.Fatalf("expected invalid port parse error")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeEnvParse {
		t.Fatalf("expected env_parse code, got %s", runtimeErr.Code)
	}
	if runtimeErr.Field != envPortKey {
		t.Fatalf("expected field %q, got %q", envPortKey, runtimeErr.Field)
	}
	if !strings.Contains(runtimeErr.Error(), "invalid integer value for PORT") {
		t.Fatalf("expected actionable integer parse message, got: %v", runtimeErr.Error())
	}

	apiPort, apiProvided, apiErr := envFile.APIPort()
	if apiErr != nil {
		t.Fatalf("expected valid API port, got %v", apiErr)
	}
	if !apiProvided || apiPort != 9091 {
		t.Fatalf("expected api_port=9091, got provided=%t value=%d", apiProvided, apiPort)
	}
}

func TestRedactEnvValueMatchesExecSensitiveKeyPolicy(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{name: "auth prefix keys are redacted", key: "AUTH_HEADER", value: "Bearer abc", expected: "[redacted]"},
		{name: "cookie keys are redacted", key: "COOKIE", value: "sessionid=abc", expected: "[redacted]"},
		{name: "session keys are redacted", key: "SESSION_ID", value: "session-123", expected: "[redacted]"},
		{name: "api key keys are redacted", key: "API_KEY", value: "apikey-123", expected: "[redacted]"},
		{name: "private key keys are redacted", key: "PRIVATE_KEY", value: "private-value", expected: "[redacted]"},
		{name: "credential keys are redacted", key: "SERVICE_CREDENTIAL", value: "credential-value", expected: "[redacted]"},
		{name: "proxy links are redacted by value", key: "STARTUP_LINK", value: "tg://proxy?server=127.0.0.1&port=443&secret=abcdef", expected: "[redacted-proxy-link]"},
		{name: "query secrets are redacted for non-sensitive keys", key: "CONTROL_URL", value: "http://127.0.0.1:9091?token=secret", expected: "http://127.0.0.1:9091?token=[redacted]"},
		{name: "proxy userinfo is redacted for non-sensitive keys", key: "HTTPS_PROXY", value: "http://user:pass@proxy.internal:3128", expected: "http://[redacted]@proxy.internal:3128"},
		{name: "non sensitive key remains visible", key: "PORT", value: "8443", expected: "8443"},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := redactEnvValue(testCase.key, testCase.value); got != testCase.expected {
				t.Fatalf("unexpected redaction result for %q: got %q, want %q", testCase.key, got, testCase.expected)
			}
		})
	}
}
