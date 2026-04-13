package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
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

func TestExecuteHelpIncludesStatusLinkLogsAndRestart(t *testing.T) {
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
	if !strings.Contains(stdout.String(), "logs") {
		t.Fatalf("expected help to include logs command, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "restart") {
		t.Fatalf("expected help to include restart command, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "uninstall") {
		t.Fatalf("expected help to include uninstall command, got: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Placeholder command (not implemented)") {
		t.Fatalf("expected uninstall command to be implemented, got: %s", stdout.String())
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

func TestExecuteStatusDispatchSmoke(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	link := "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{}, nil
	}
	newTelemtAPI = func(*runtime.RuntimeInstallation, *slog.Logger) (telemtStatusAPI, error) {
		return &stubTelemtStatusAPI{endpoint: "http://127.0.0.1:9091"}, nil
	}
	collectTelemtStatus = func(context.Context, telemt.StatusCollectorOptions) (telemt.StatusSummary, error) {
		return telemt.StatusSummary{
			Provider:      runtime.ProviderTelemt,
			RuntimeStatus: telemt.RuntimeStatusHealthy,
			ComposeStatus: telemt.ComposeStatusRunning,
			HealthStatus:  telemt.HealthStatusHealthy,
			LinkStatus:    telemt.LinkStatusAvailable,
			LinkReason:    "link_ok",
			ProxyLink:     link,
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute([]string{"status"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected status command success, got %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Runtime status: healthy") {
		t.Fatalf("expected healthy runtime status in output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "[redacted-proxy-link]") {
		t.Fatalf("expected redacted link marker in status output, got: %s", rendered)
	}

	logText := stderr.String()
	if !strings.Contains(logText, "selected subcommand") {
		t.Fatalf("expected subcommand selection log, got: %s", logText)
	}
	if !strings.Contains(logText, "status command entry") {
		t.Fatalf("expected status command entry log, got: %s", logText)
	}
	if !strings.Contains(logText, "final runtime summary") {
		t.Fatalf("expected final summary log, got: %s", logText)
	}
	if strings.Contains(logText, link) {
		t.Fatalf("expected status logs to keep proxy link redacted, got: %s", logText)
	}
}

func TestExecuteLinkDispatchSmoke(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	link := "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{}, nil
	}
	newTelemtAPI = func(*runtime.RuntimeInstallation, *slog.Logger) (telemtStatusAPI, error) {
		return &stubTelemtStatusAPI{endpoint: "http://127.0.0.1:9091"}, nil
	}
	collectTelemtStatus = func(context.Context, telemt.StatusCollectorOptions) (telemt.StatusSummary, error) {
		return telemt.StatusSummary{
			Provider:      runtime.ProviderTelemt,
			RuntimeStatus: telemt.RuntimeStatusHealthy,
			ComposeStatus: telemt.ComposeStatusRunning,
			HealthStatus:  telemt.HealthStatusHealthy,
			LinkStatus:    telemt.LinkStatusAvailable,
			LinkReason:    "link_ok",
			ProxyLink:     link,
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute([]string{"link"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected link command success, got %v", err)
	}

	if strings.TrimSpace(stdout.String()) != link {
		t.Fatalf("expected full proxy link in stdout, got: %s", stdout.String())
	}
	logText := stderr.String()
	if !strings.Contains(logText, "link command resolved") {
		t.Fatalf("expected link command resolution log, got: %s", logText)
	}
	if strings.Contains(logText, link) {
		t.Fatalf("expected link command logs to keep proxy link redacted, got: %s", logText)
	}
}

func TestExecuteLogsDispatchSmoke(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	capture := &capturingCLIComposeRunner{}
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return capture, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute([]string{"logs", "--tail", "5"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected logs command success, got %v", err)
	}

	if capture.lastCommand.Subcommand != "logs" {
		t.Fatalf("expected compose logs subcommand, got %q", capture.lastCommand.Subcommand)
	}
	if len(capture.lastCommand.Services) != 1 || capture.lastCommand.Services[0] != "mtg" {
		t.Fatalf("expected default mtg service selection, got: %#v", capture.lastCommand.Services)
	}
	if !strings.Contains(stdout.String(), "streamed line") {
		t.Fatalf("expected streamed logs output, got: %s", stdout.String())
	}
	logText := stderr.String()
	if !strings.Contains(logText, "logs command start") {
		t.Fatalf("expected logs command start log, got: %s", logText)
	}
	if !strings.Contains(logText, "logs command finish") {
		t.Fatalf("expected logs command finish log, got: %s", logText)
	}
}

func TestExecuteRestartDispatchSmoke(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")

	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	queue := &queueCLIComposeRunner{
		responses: []queueCLIComposeResponse{
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\nmtg image Up 1 minute",
				},
			},
			{
				result: execadapter.Result{
					ExitCode: 0,
				},
			},
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\nmtg image Up 2 seconds",
				},
			},
		},
	}
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return queue, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Execute([]string{"restart", "--timeout", "5"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected restart command success, got %v", err)
	}

	if len(queue.calls) != 3 {
		t.Fatalf("expected pre/restart/post compose calls, got %d", len(queue.calls))
	}
	if !strings.Contains(stdout.String(), "Restart result: healthy") {
		t.Fatalf("expected healthy restart summary, got: %s", stdout.String())
	}
	logText := stderr.String()
	if !strings.Contains(logText, "restart command begin") {
		t.Fatalf("expected restart begin log, got: %s", logText)
	}
	if !strings.Contains(logText, "restart command end") {
		t.Fatalf("expected restart end log, got: %s", logText)
	}
}

func TestExecuteLifecycleWrappersDispatchSmoke(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")
	t.Setenv("CI", "")
	t.Setenv("SUDO_UID", "")
	t.Setenv("SUDO_USER", "")

	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}

	stub := &lifecycleManagerStub{
		installResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nPublic endpoint: 198.51.100.20:443\nSecret: supersecret\nProxy link:\ntg://proxy?server=198.51.100.20&port=443&secret=supersecret\nAPI: http://127.0.0.1:9091/v1/health\nConfig: /opt/mtproxy-installer/providers/telemt/telemt.toml\n",
		},
		updateResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\nImage is already up to date: ghcr.io/example/telemt@sha256:abc\ntelemt update complete\nProvider: telemt\nSource: ghcr.io/example/telemt:stable\nActive: ghcr.io/example/telemt@sha256:abc\nHealth: curl http://127.0.0.1:9091/v1/health\n",
		},
		uninstallResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nStrategy: telemt_only\nProvider: telemt\nKeep data: true\nCleanup status: completed_keep_data\nData removed: false\nImage cleanup: removed\nOutcome: Telemt runtime removed; install directory preserved\n",
		},
	}
	withLifecycleManagerStub(t, stub)

	var installStdout bytes.Buffer
	var installStderr bytes.Buffer
	err := Execute([]string{"install", "--provider", "telemt"}, &installStdout, &installStderr)
	if err != nil {
		t.Fatalf("expected install command success, got %v", err)
	}
	if !strings.Contains(installStdout.String(), "Install status: installed") {
		t.Fatalf("expected structured install output, got: %s", installStdout.String())
	}
	if strings.Contains(installStdout.String(), "supersecret") || strings.Contains(installStdout.String(), "tg://proxy?") {
		t.Fatalf("expected install stdout to stay redacted, got: %s", installStdout.String())
	}

	var updateStdout bytes.Buffer
	var updateStderr bytes.Buffer
	err = Execute([]string{"update"}, &updateStdout, &updateStderr)
	if err != nil {
		t.Fatalf("expected update command success, got %v", err)
	}
	if !strings.Contains(updateStdout.String(), "Update status: already_up_to_date") {
		t.Fatalf("expected structured update output, got: %s", updateStdout.String())
	}

	var uninstallStdout bytes.Buffer
	var uninstallStderr bytes.Buffer
	err = Execute([]string{"uninstall", "--yes", "--keep-data"}, &uninstallStdout, &uninstallStderr)
	if err != nil {
		t.Fatalf("expected uninstall command success, got %v", err)
	}
	if !strings.Contains(uninstallStdout.String(), "Uninstall status: completed_keep_data") {
		t.Fatalf("expected structured uninstall output, got: %s", uninstallStdout.String())
	}
	if !strings.Contains(uninstallStderr.String(), "uninstall lifecycle finish") {
		t.Fatalf("expected uninstall completion logs, got: %s", uninstallStderr.String())
	}
}

func TestExecuteFailureLogsActionableAndRedactedContext(t *testing.T) {
	resetVersionState(t, "dev", "unknown", "unknown", "development")
	t.Setenv(logLevelEnv, "debug")
	t.Setenv("CI", "")
	t.Setenv("SUDO_UID", "")
	t.Setenv("SUDO_USER", "")

	stub := &lifecycleManagerStub{
		installResult: execadapter.Result{
			StderrSummary: "bootstrap failure API_KEY=super-secret",
		},
		installErr: errors.New("install failed for tg://proxy?server=127.0.0.1&port=443&secret=abcdef"),
	}
	withLifecycleManagerStub(t, stub)

	var stderr bytes.Buffer
	err := Execute([]string{"install"}, io.Discard, &stderr)
	if err == nil {
		t.Fatalf("expected install failure")
	}

	logText := stderr.String()
	if !strings.Contains(logText, "install lifecycle failed") {
		t.Fatalf("expected actionable install failure context, got: %s", logText)
	}
	if !strings.Contains(logText, "command failed") {
		t.Fatalf("expected top-level command failure log, got: %s", logText)
	}
	if strings.Contains(logText, "super-secret") {
		t.Fatalf("expected secrets to be redacted in failure logs, got: %s", logText)
	}
	if strings.Contains(logText, "tg://proxy?") {
		t.Fatalf("expected proxy link to be redacted in failure logs, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted]") || !strings.Contains(logText, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction markers in failure logs, got: %s", logText)
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
