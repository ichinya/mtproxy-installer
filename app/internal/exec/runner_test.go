package exec

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestBuildCommandEnvPreservesPathAndRequiresExplicitDockerOptIn(t *testing.T) {
	base := []string{
		"PATH=/custom/bin:/usr/bin",
		"DOCKER_HOST=tcp://docker.internal:2375",
		"HTTP_PROXY=http://proxy.internal:3128",
	}
	allowlist := []string{"PATH", "DOCKER_HOST", "HTTP_PROXY"}

	env, blocked := buildCommandEnv(base, allowlist, nil, map[string]string{
		"NO_PROXY":    "localhost,127.0.0.1",
		"DOCKER_HOST": "tcp://docker.internal:2375",
	}, false, "")
	envMap := envListToMap(env)

	if got := envMap["PATH"]; got != "/custom/bin:/usr/bin" {
		t.Fatalf("expected PATH to be preserved, got %q", got)
	}
	if got := envMap["DOCKER_HOST"]; got != "tcp://docker.internal:2375" {
		t.Fatalf("expected explicit DOCKER_HOST override to be preserved, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if _, ok := envMap["NO_PROXY"]; ok {
		t.Fatalf("did not expect NO_PROXY without allowlist entry")
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvDoesNotImplicitlyInheritParentPath(t *testing.T) {
	t.Setenv("PATH", "/tmp/inherited/bin")

	env, blocked := buildCommandEnv(nil, nil, nil, nil, false, "")
	envMap := envListToMap(env)

	if got := envMap["PATH"]; got != normalizedExecutablePath {
		t.Fatalf("expected PATH fallback to normalized executable path, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvUsesNormalizedPathWhenPathNotAllowlisted(t *testing.T) {
	t.Setenv("PATH", "/tmp/inherited/bin")

	env, blocked := buildCommandEnv(
		[]string{"HTTP_PROXY=http://proxy.internal:3128"},
		[]string{"HTTP_PROXY"},
		nil,
		nil,
		false,
		"",
	)
	envMap := envListToMap(env)

	if got := envMap["PATH"]; got != normalizedExecutablePath {
		t.Fatalf("expected PATH to use normalized executable path when PATH is not allowlisted, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestFilterInheritedSensitiveRuntimeEnvRemovesPrivilegedDefaults(t *testing.T) {
	filtered, stripped := filterInheritedSensitiveRuntimeEnv([]string{
		"PATH=/custom/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.internal:3128",
		"HOME=/root",
		"XDG_RUNTIME_DIR=/run/user/0",
		"DOCKER_HOST=tcp://docker.internal:2375",
		"DOCKER_CONTEXT=prod",
		"DOCKER_TLS=1",
		"DOCKER_CERT_PATH=/etc/docker/certs",
		"DOCKER_TLS_CERTDIR=/certs",
		"DOCKER_TLS_VERIFY=1",
		"COMPOSE_PROJECT_NAME=mtproxy",
		"COMPOSE_PROFILES=prod",
	})
	envMap := envListToMap(filtered)

	if got := envMap["PATH"]; got != "/custom/bin:/usr/bin" {
		t.Fatalf("expected PATH to be preserved, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if _, ok := envMap["HOME"]; ok {
		t.Fatalf("expected HOME to be stripped from inherited parent env")
	}
	if _, ok := envMap["XDG_RUNTIME_DIR"]; ok {
		t.Fatalf("expected XDG_RUNTIME_DIR to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_HOST"]; ok {
		t.Fatalf("expected DOCKER_HOST to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_CONTEXT"]; ok {
		t.Fatalf("expected DOCKER_CONTEXT to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_TLS"]; ok {
		t.Fatalf("expected DOCKER_TLS to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_CERT_PATH"]; ok {
		t.Fatalf("expected DOCKER_CERT_PATH to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_TLS_CERTDIR"]; ok {
		t.Fatalf("expected DOCKER_TLS_CERTDIR to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_TLS_VERIFY"]; ok {
		t.Fatalf("expected DOCKER_TLS_VERIFY to be stripped from inherited parent env")
	}
	if _, ok := envMap["COMPOSE_PROJECT_NAME"]; ok {
		t.Fatalf("expected COMPOSE_PROJECT_NAME to be stripped from inherited parent env")
	}
	if _, ok := envMap["COMPOSE_PROFILES"]; ok {
		t.Fatalf("expected COMPOSE_PROFILES to be stripped from inherited parent env")
	}

	expectedStripped := []string{
		"COMPOSE_PROFILES",
		"COMPOSE_PROJECT_NAME",
		"DOCKER_CERT_PATH",
		"DOCKER_CONTEXT",
		"DOCKER_HOST",
		"DOCKER_TLS",
		"DOCKER_TLS_CERTDIR",
		"DOCKER_TLS_VERIFY",
		"HOME",
		"XDG_RUNTIME_DIR",
	}
	if len(stripped) != len(expectedStripped) {
		t.Fatalf("expected stripped key count %d, got %d (%v)", len(expectedStripped), len(stripped), stripped)
	}
	for idx, key := range expectedStripped {
		if stripped[idx] != key {
			t.Fatalf("unexpected stripped key order/content at %d: got %q want %q", idx, stripped[idx], key)
		}
	}
}

func TestBuildCommandEnvBlocksDangerousShellEnv(t *testing.T) {
	env, blocked := buildCommandEnv(
		[]string{"PATH=/usr/bin"},
		[]string{"PATH", "BASH_ENV"},
		nil,
		map[string]string{
			"BASH_ENV": "/tmp/evil.sh",
		},
		false,
		"",
	)

	envMap := envListToMap(env)
	if _, ok := envMap["BASH_ENV"]; ok {
		t.Fatalf("expected BASH_ENV to be blocked")
	}
	if len(blocked) == 0 {
		t.Fatalf("expected blocked env keys to include BASH_ENV")
	}
}

func TestRunRejectsUnsupportedEnvOverrides(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:          "echo",
		Args:             []string{"ok"},
		WorkingDir:       ".",
		InheritParentEnv: true,
		AllowedEnvKeys:   []string{"PATH"},
		EnvOverrides: map[string]string{
			"INSTALL_DIR": "/opt/mtproxy-installer",
		},
	})
	if err == nil {
		t.Fatalf("expected unsupported env override to fail")
	}
	if !strings.Contains(err.Error(), "env override keys are not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsEnvOverridesWithoutAllowlist(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"INSTALL_DIR": "/opt/mtproxy-installer",
		},
	})
	if err == nil {
		t.Fatalf("expected env overrides without allowlist to fail")
	}
	if !strings.Contains(err.Error(), "AllowedEnvKeys or AllowedEnvPrefixes is required when EnvOverrides are provided") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsSensitiveInheritedAllowlistSelection(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:          "echo",
		Args:             []string{"ok"},
		WorkingDir:       ".",
		InheritParentEnv: true,
		AllowedEnvKeys:   []string{"PATH", "DOCKER_HOST"},
	})
	if err == nil {
		t.Fatalf("expected sensitive inherited allowlist selection to fail")
	}
	if !strings.Contains(err.Error(), "inherited sensitive env selection is not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsSensitiveInheritedPrefixSelection(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:            "echo",
		Args:               []string{"ok"},
		WorkingDir:         ".",
		InheritParentEnv:   true,
		AllowedEnvKeys:     []string{"PATH"},
		AllowedEnvPrefixes: []string{"DOCKER_TLS_"},
	})
	if err == nil {
		t.Fatalf("expected sensitive inherited prefix selection to fail")
	}
	if !strings.Contains(err.Error(), "inherited sensitive env selection is not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsSensitiveOverrideWithoutExplicitKeyAllowlist(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"DOCKER_TLS_CERTDIR": "/certs",
		},
		AllowedEnvPrefixes: []string{"DOCKER_TLS_"},
	})
	if err == nil {
		t.Fatalf("expected sensitive override without explicit key allowlist to fail")
	}
	if !strings.Contains(err.Error(), "sensitive env override keys require explicit AllowedEnvKeys opt-in") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAllowsSensitiveOverrideWithExplicitKeyAllowlist(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"DOCKER_TLS_CERTDIR": "/certs",
		},
		AllowedEnvKeys: []string{"DOCKER_TLS_CERTDIR"},
	})
	if err != nil {
		t.Fatalf("expected explicit sensitive key allowlist to pass, got %v", err)
	}
}

func TestRunRejectsUnsafeEnvOverrideValue(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"INSTALL_DIR": "/opt/mtproxy-installer\nBAD=1",
		},
		AllowedEnvKeys: []string{"INSTALL_DIR"},
	})
	if err == nil {
		t.Fatalf("expected unsafe env override value to fail")
	}
	if !strings.Contains(err.Error(), "contains unsafe characters") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildCommandEnvUsesSafePathForPrivilegedRun(t *testing.T) {
	env, blocked := buildCommandEnv(
		[]string{"PATH=/custom/bin", "HTTP_PROXY=http://proxy.internal:3128"},
		[]string{"PATH", "HTTP_PROXY"},
		nil,
		map[string]string{
			"PATH":       "/tmp/evil",
			"HTTP_PROXY": "http://proxy.internal:3128",
		},
		true,
		"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)

	envMap := envListToMap(env)
	if got := envMap["PATH"]; got != "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Fatalf("expected safe PATH to override inherited/override values, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvClearsPrivilegedEnvByDefault(t *testing.T) {
	allowlist := []string{
		"HOME",
		"XDG_RUNTIME_DIR",
		"DOCKER_HOST",
		"DOCKER_CONTEXT",
		"DOCKER_TLS_VERIFY",
		"COMPOSE_PROJECT_NAME",
		"COMPOSE_PROFILES",
	}

	env, blocked := buildCommandEnv(
		nil,
		allowlist,
		[]string{"DOCKER_TLS_"},
		map[string]string{
			"DOCKER_HOST": "tcp://docker.internal:2375",
		},
		true,
		"",
	)
	envMap := envListToMap(env)

	if got := envMap["DOCKER_HOST"]; got != "tcp://docker.internal:2375" {
		t.Fatalf("expected explicit DOCKER_HOST override to be preserved, got %q", got)
	}
	if _, ok := envMap["HOME"]; ok {
		t.Fatalf("expected HOME to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["XDG_RUNTIME_DIR"]; ok {
		t.Fatalf("expected XDG_RUNTIME_DIR to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["DOCKER_CONTEXT"]; ok {
		t.Fatalf("expected DOCKER_CONTEXT to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["DOCKER_TLS_VERIFY"]; ok {
		t.Fatalf("expected DOCKER_TLS_VERIFY to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["COMPOSE_PROJECT_NAME"]; ok {
		t.Fatalf("expected COMPOSE_PROJECT_NAME to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["COMPOSE_PROFILES"]; ok {
		t.Fatalf("expected COMPOSE_PROFILES to be cleared when parent env inheritance is disabled")
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvAllowsPrefixOptInOverrides(t *testing.T) {
	env, blocked := buildCommandEnv(
		nil,
		[]string{"PATH"},
		[]string{"DOCKER_TLS_"},
		map[string]string{
			"DOCKER_TLS_CERTDIR": "/certs",
		},
		true,
		"",
	)
	envMap := envListToMap(env)

	if got := envMap["DOCKER_TLS_CERTDIR"]; got != "/certs" {
		t.Fatalf("expected DOCKER_TLS_CERTDIR override to be preserved, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestRunnerRunCapturesOutputAndLogsLifecycle(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewRunner(logger)

	result, err := runner.Run(context.Background(), helperProcessRequest(
		"success",
		"helper stdout",
		"helper stderr Authorization: Bearer abc123",
	))
	if err != nil {
		t.Fatalf("expected helper process success, got %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "helper stdout") {
		t.Fatalf("expected captured stdout, got %q", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "helper stderr") {
		t.Fatalf("expected captured stderr, got %q", result.Stderr)
	}
	if strings.Contains(result.StderrSummary, "abc123") {
		t.Fatalf("expected redacted stderr summary, got %q", result.StderrSummary)
	}
	if !strings.Contains(strings.ToLower(result.StderrSummary), "[redacted]") {
		t.Fatalf("expected redaction marker in stderr summary, got %q", result.StderrSummary)
	}

	logText := logs.String()
	if !strings.Contains(logText, "external command start") {
		t.Fatalf("expected command-start log, got: %s", logText)
	}
	if !strings.Contains(logText, "external command finish") {
		t.Fatalf("expected command-finish log, got: %s", logText)
	}
	if !strings.Contains(logText, "elapsed=") {
		t.Fatalf("expected elapsed field in logs, got: %s", logText)
	}
	if !strings.Contains(logText, "exit_status=0") {
		t.Fatalf("expected exit_status field in logs, got: %s", logText)
	}
	if strings.Contains(logText, "abc123") {
		t.Fatalf("expected sensitive stderr to stay redacted in logs, got: %s", logText)
	}
}

func TestRunnerRunFailureReturnsExitCodeAndRedactedError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewRunner(logger)

	proxyLink := "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"
	result, err := runner.Run(context.Background(), helperProcessRequest(
		"fail",
		"",
		"stderr API_KEY=super-secret "+proxyLink,
	))
	if err == nil {
		t.Fatalf("expected helper process failure")
	}
	if result.ExitCode != 17 {
		t.Fatalf("expected exit code 17, got %d", result.ExitCode)
	}

	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("expected CommandError, got %T", err)
	}
	errorText := commandErr.Error()
	if !strings.Contains(errorText, "external command failed") {
		t.Fatalf("expected actionable command failure error, got %q", errorText)
	}
	if strings.Contains(errorText, "super-secret") || strings.Contains(errorText, proxyLink) {
		t.Fatalf("expected command error to redact sensitive values, got %q", errorText)
	}
	if !strings.Contains(errorText, "[redacted]") || !strings.Contains(errorText, "[redacted-proxy-link]") {
		t.Fatalf("expected command error to include redaction markers, got %q", errorText)
	}

	logText := logs.String()
	if !strings.Contains(logText, "external command finish") {
		t.Fatalf("expected command-finish log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR level for failed command, got: %s", logText)
	}
	if !strings.Contains(logText, "exit_status=17") {
		t.Fatalf("expected exit_status=17 in logs, got: %s", logText)
	}
	if strings.Contains(logText, "super-secret") || strings.Contains(logText, proxyLink) {
		t.Fatalf("expected logs to redact sensitive values, got: %s", logText)
	}
}

func TestRunnerRunSupportsCaptureToggles(t *testing.T) {
	runner := NewRunner(nil)

	var streamStdout bytes.Buffer
	var streamStderr bytes.Buffer
	result, err := runner.Run(context.Background(), Request{
		Command:              os.Args[0],
		Args:                 []string{"-test.run=TestRunnerHelperProcess"},
		WorkingDir:           ".",
		EnvOverrides:         helperEnv("success", "stdout-stream", "stderr-stream"),
		AllowedEnvKeys:       helperAllowedEnvKeys(),
		DisableStdoutCapture: true,
		DisableStderrCapture: true,
		Stdout:               &streamStdout,
		Stderr:               &streamStderr,
	})
	if err != nil {
		t.Fatalf("expected helper process success, got %v", err)
	}

	if result.Stdout != "" {
		t.Fatalf("expected stdout capture to be disabled, got %q", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("expected stderr capture to be disabled, got %q", result.Stderr)
	}
	if result.StderrSummary != "" {
		t.Fatalf("expected stderr summary to be empty when stderr capture is disabled, got %q", result.StderrSummary)
	}
	if !strings.Contains(streamStdout.String(), "stdout-stream") {
		t.Fatalf("expected streamed stdout output, got %q", streamStdout.String())
	}
	if !strings.Contains(streamStderr.String(), "stderr-stream") {
		t.Fatalf("expected streamed stderr output, got %q", streamStderr.String())
	}
}

func TestSummarizeStderrTruncatesAndRedacts(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		"line one API_KEY=super-secret",
		"line two Authorization: Bearer abc123",
		"line three tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
		"line four should not appear",
	}, "\n")
	summary := SummarizeStderr(raw, 90)

	if strings.Contains(summary, "super-secret") || strings.Contains(summary, "abc123") {
		t.Fatalf("expected summary to redact sensitive tokens, got %q", summary)
	}
	if strings.Contains(summary, "tg://proxy?") {
		t.Fatalf("expected summary to redact proxy links, got %q", summary)
	}
	if strings.Contains(summary, "line four should not appear") {
		t.Fatalf("expected summary to use first three lines only, got %q", summary)
	}
	if len([]rune(summary)) > 90 {
		t.Fatalf("expected summary to honor truncation limit, got %d runes", len([]rune(summary)))
	}
	if !strings.Contains(summary, "...") {
		t.Fatalf("expected long summary to be truncated with ellipsis, got %q", summary)
	}
}

func TestRedactArgsAndRedactTextHideSensitiveValues(t *testing.T) {
	t.Parallel()

	redactedArgs := RedactArgs([]string{
		"--secret=secret-value",
		"--token",
		"token-value",
		"tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
		"plain-value",
	})
	if got := redactedArgs[0]; got != "--secret=[redacted]" {
		t.Fatalf("expected inline secret redaction, got %q", got)
	}
	if got := redactedArgs[1]; got != "--token" {
		t.Fatalf("expected sensitive flag marker to stay visible, got %q", got)
	}
	if got := redactedArgs[2]; got != "[redacted]" {
		t.Fatalf("expected token argument value to be redacted, got %q", got)
	}
	if got := redactedArgs[3]; got != "[redacted-proxy-link]" {
		t.Fatalf("expected proxy link argument to be redacted, got %q", got)
	}
	if got := redactedArgs[4]; got != "plain-value" {
		t.Fatalf("expected plain argument to stay visible, got %q", got)
	}

	redactedText := RedactText(`Authorization: Bearer abc123 Cookie: sid=xyz api_key=secret query_token=123 https_proxy=http://user:pass@proxy.internal:3128`)
	if strings.Contains(redactedText, "abc123") || strings.Contains(redactedText, "sid=xyz") || strings.Contains(redactedText, "secret") || strings.Contains(redactedText, "123") || strings.Contains(redactedText, "user:pass@") {
		t.Fatalf("expected redacted text to hide secrets, got %q", redactedText)
	}
	if !strings.Contains(strings.ToLower(redactedText), "[redacted]") {
		t.Fatalf("expected redaction marker in text, got %q", redactedText)
	}
	if !strings.Contains(redactedText, "http://[redacted]@proxy.internal:3128") {
		t.Fatalf("expected proxy URL userinfo to be redacted, got %q", redactedText)
	}
}

func TestRedactEnvSnapshotRedactsProxyCredentials(t *testing.T) {
	t.Parallel()

	redacted := RedactEnvSnapshot(map[string]string{
		"HTTPS_PROXY": "http://user:pass@proxy.internal:3128",
		"HTTP_PROXY":  "http://token@proxy.internal:3128",
		"NO_PROXY":    "localhost,127.0.0.1",
	})

	if got := redacted["HTTPS_PROXY"]; got != "http://[redacted]@proxy.internal:3128" {
		t.Fatalf("expected HTTPS_PROXY userinfo redaction, got %q", got)
	}
	if got := redacted["HTTP_PROXY"]; got != "http://[redacted]@proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY userinfo redaction, got %q", got)
	}
	if got := redacted["NO_PROXY"]; got != "localhost,127.0.0.1" {
		t.Fatalf("expected NO_PROXY to stay visible, got %q", got)
	}
}

func TestCommandErrorIncludesRedactedStderrSummary(t *testing.T) {
	t.Parallel()

	commandErr := &CommandError{
		Result: Result{
			StderrSummary: "API_KEY=super-secret tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
		},
		Err: errors.New("exit status 1 token=abc123"),
	}
	errorText := commandErr.Error()
	if !strings.Contains(errorText, "external command failed") {
		t.Fatalf("expected external command failure prefix, got %q", errorText)
	}
	if strings.Contains(errorText, "super-secret") || strings.Contains(errorText, "abc123") || strings.Contains(errorText, "tg://proxy?") {
		t.Fatalf("expected command error to redact sensitive values, got %q", errorText)
	}
	if !strings.Contains(errorText, "[redacted]") || !strings.Contains(errorText, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction markers in command error text, got %q", errorText)
	}
}

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	if _, err := os.Stdout.WriteString(os.Getenv("RUNNER_HELPER_STDOUT")); err != nil {
		os.Exit(11)
	}
	if _, err := os.Stderr.WriteString(os.Getenv("RUNNER_HELPER_STDERR")); err != nil {
		os.Exit(12)
	}

	if os.Getenv("RUNNER_HELPER_MODE") == "fail" {
		os.Exit(17)
	}
	os.Exit(0)
}

func helperProcessRequest(mode string, stdout string, stderr string) Request {
	return Request{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestRunnerHelperProcess"},
		WorkingDir:     ".",
		EnvOverrides:   helperEnv(mode, stdout, stderr),
		AllowedEnvKeys: helperAllowedEnvKeys(),
		LogSuccess:     true,
	}
}

func helperEnv(mode string, stdout string, stderr string) map[string]string {
	return map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"RUNNER_HELPER_MODE":     strings.TrimSpace(mode),
		"RUNNER_HELPER_STDOUT":   stdout,
		"RUNNER_HELPER_STDERR":   stderr,
	}
}

func helperAllowedEnvKeys() []string {
	return []string{
		"GO_WANT_HELPER_PROCESS",
		"RUNNER_HELPER_MODE",
		"RUNNER_HELPER_STDOUT",
		"RUNNER_HELPER_STDERR",
	}
}

func envListToMap(entries []string) map[string]string {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		result[key] = value
	}
	return result
}
