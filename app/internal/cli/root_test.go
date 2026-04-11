package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"mtproxy-installer/app/internal/version"
)

func TestExecuteVersionCommand(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Execute([]string{"version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	logs := stderr.String()
	if !strings.Contains(logs, "cli startup") {
		t.Fatalf("expected startup log, got: %s", logs)
	}
	if !strings.Contains(logs, "resolved build info") {
		t.Fatalf("expected build info log, got: %s", logs)
	}
	if !strings.Contains(logs, "selected subcommand") {
		t.Fatalf("expected subcommand log, got: %s", logs)
	}
	if !strings.Contains(logs, "command dispatch start") {
		t.Fatalf("expected debug dispatch log in dev mode, got: %s", logs)
	}

	if !strings.Contains(stdout.String(), "version=dev") {
		t.Fatalf("expected version output, got: %s", stdout.String())
	}
}

func TestExecuteReturnsFatalConfigErrorForInvalidLogLevel(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "trace")

	var stderr bytes.Buffer

	err := Execute([]string{"version"}, io.Discard, &stderr)
	if err == nil {
		t.Fatalf("expected error for invalid log level")
	}

	var cfgErr *FatalConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected FatalConfigError, got %T", err)
	}

	logs := stderr.String()
	if !strings.Contains(logs, "fatal configuration error") {
		t.Fatalf("expected fatal config error log, got: %s", logs)
	}
}

func TestExecuteRedactsProxyLinksForNonLinkCommands(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	var stderr bytes.Buffer

	err := Execute([]string{"tg://proxy?server=127.0.0.1&secret=abcdef"}, io.Discard, &stderr)
	if err == nil {
		t.Fatalf("expected unknown command error")
	}

	logs := stderr.String()
	if strings.Contains(logs, "tg://proxy?") {
		t.Fatalf("expected proxy link to be redacted, got: %s", logs)
	}
	if strings.Contains(logs, "secret=abcdef") {
		t.Fatalf("expected secret to be redacted, got: %s", logs)
	}
	if !strings.Contains(logs, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction marker in logs, got: %s", logs)
	}
}

func TestRedactForCommandRedactsLinkCommand(t *testing.T) {
	raw := "tg://proxy?server=127.0.0.1&secret=abcdef"
	got := redactForCommand("link", raw)
	if strings.Contains(got, "tg://proxy?") {
		t.Fatalf("expected link command logs to stay redacted, got: %s", got)
	}
	if strings.Contains(got, "secret=abcdef") {
		t.Fatalf("expected link command secret to be redacted, got: %s", got)
	}
	if !strings.Contains(got, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction marker, got: %s", got)
	}
}

func TestRedactForCommandRedactsBearerCookieAndJSONSecrets(t *testing.T) {
	raw := `Authorization: Bearer abc123 Cookie: session=abcdef {"api_key":"secret-value","authToken":"token-value"}`
	got := redactForCommand("status", raw)

	if strings.Contains(got, "abc123") {
		t.Fatalf("expected bearer token to be redacted, got: %s", got)
	}
	if strings.Contains(got, "session=abcdef") {
		t.Fatalf("expected cookie value to be redacted, got: %s", got)
	}
	if strings.Contains(got, "secret-value") || strings.Contains(got, "token-value") {
		t.Fatalf("expected JSON secrets to be redacted, got: %s", got)
	}
	if !strings.Contains(strings.ToLower(got), "authorization: [redacted]") {
		t.Fatalf("expected authorization header to be redacted, got: %s", got)
	}
}

func TestExecuteHelpIncludesStatusAndLink(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Execute([]string{"help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !strings.Contains(stdout.String(), "status") {
		t.Fatalf("expected help to include status command, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "link") {
		t.Fatalf("expected help to include link command, got: %s", stdout.String())
	}
}

func TestExecuteStatusRejectsUnexpectedArgs(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	err := Execute([]string{"status", "unexpected"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("expected status command to reject unexpected args")
	}
	if !strings.Contains(err.Error(), "status command does not accept arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteLinkRejectsUnexpectedArgs(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	err := Execute([]string{"link", "unexpected"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("expected link command to reject unexpected args")
	}
	if !strings.Contains(err.Error(), "link command does not accept arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func resetVersionState(t *testing.T, ver string, commit string, buildDate string, buildMode string) {
	t.Helper()

	oldVersion := version.Version
	oldCommit := version.Commit
	oldBuildDate := version.BuildDate
	oldBuildMode := version.BuildMode

	version.Version = ver
	version.Commit = commit
	version.BuildDate = buildDate
	version.BuildMode = buildMode

	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.BuildDate = oldBuildDate
		version.BuildMode = oldBuildMode
	})
}
