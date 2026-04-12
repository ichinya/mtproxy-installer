package cli

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/scripts"
)

type lifecycleManagerStub struct {
	installResult   execadapter.Result
	installErr      error
	installRequests []scripts.InstallOptions

	updateResult   execadapter.Result
	updateErr      error
	updateRequests []scripts.UpdateOptions

	uninstallResult   execadapter.Result
	uninstallErr      error
	uninstallRequests []scripts.UninstallOptions
}

func (s *lifecycleManagerStub) Install(_ context.Context, options scripts.InstallOptions) (execadapter.Result, error) {
	s.installRequests = append(s.installRequests, options)
	return s.installResult, s.installErr
}

func (s *lifecycleManagerStub) Update(_ context.Context, options scripts.UpdateOptions) (execadapter.Result, error) {
	s.updateRequests = append(s.updateRequests, options)
	return s.updateResult, s.updateErr
}

func (s *lifecycleManagerStub) Uninstall(_ context.Context, options scripts.UninstallOptions) (execadapter.Result, error) {
	s.uninstallRequests = append(s.uninstallRequests, options)
	return s.uninstallResult, s.uninstallErr
}

func withLifecycleManagerStub(t *testing.T, stub *lifecycleManagerStub) {
	t.Helper()

	old := newLifecycleScriptsManager
	newLifecycleScriptsManager = func(_ *slog.Logger) (lifecycleScriptsManager, error) {
		return stub, nil
	}
	t.Cleanup(func() {
		newLifecycleScriptsManager = old
	})
}

func TestParseInstallCommandArgsRejectsTelemtOnlyFlagsForMTG(t *testing.T) {
	_, err := parseInstallCommandArgs([]string{"--provider", "mtg", "--api-port", "9091"})
	if err == nil {
		t.Fatalf("expected parse error for mtg + api-port")
	}
	if !strings.Contains(err.Error(), "--api-port") {
		t.Fatalf("expected api-port error, got %v", err)
	}
}

func TestParseInstallCommandArgsRejectsTrustBoundaryEnvWithoutAllowFlag(t *testing.T) {
	_, err := parseInstallCommandArgs([]string{"--env", "DOCKER_HOST=tcp://docker:2375"})
	if err == nil {
		t.Fatalf("expected parse error for trust-boundary env override without allow flag")
	}
	if !strings.Contains(err.Error(), "--allow-trust-boundary-env") {
		t.Fatalf("expected allow flag hint, got %v", err)
	}
}

func TestParseInstallCommandArgsRejectsTrustBoundaryEnvInCIContext(t *testing.T) {
	t.Setenv("CI", "true")

	_, err := parseInstallCommandArgs([]string{
		"--allow-trust-boundary-env",
		"--env", "DOCKER_HOST=tcp://docker:2375",
	})
	if err == nil {
		t.Fatalf("expected parse error in CI context")
	}
	if !strings.Contains(err.Error(), "privileged context") {
		t.Fatalf("expected privileged context marker, got %v", err)
	}
}

func TestRunInstallUsesStructuredRendererAndRedactedLogs(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("SUDO_UID", "")
	t.Setenv("SUDO_USER", "")

	stub := &lifecycleManagerStub{
		installResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nPublic endpoint: 198.51.100.20:443\nSecret: supersecret\nProxy link:\ntg://proxy?server=198.51.100.20&port=443&secret=supersecret\nAPI: http://127.0.0.1:9091/v1/health\nConfig: /opt/mtproxy-installer/providers/telemt/telemt.toml\n",
		},
	}
	withLifecycleManagerStub(t, stub)

	ctx, stdout, logs := newTestCommandContext(
		"install",
		"--provider", "telemt",
		"--port", "443",
		"--allow-trust-boundary-env",
		"--env", "DOCKER_HOST=tcp://docker:2375",
	)
	if err := runInstall(ctx); err != nil {
		t.Fatalf("expected install success, got %v", err)
	}

	if len(stub.installRequests) != 1 {
		t.Fatalf("expected one install request, got %d", len(stub.installRequests))
	}
	if stub.installRequests[0].Provider != runtime.ProviderTelemt {
		t.Fatalf("unexpected provider in request: %s", stub.installRequests[0].Provider)
	}
	if got := stub.installRequests[0].ExtraEnv["DOCKER_HOST"]; got != "tcp://docker:2375" {
		t.Fatalf("unexpected env mapping: %q", got)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Install status: installed") {
		t.Fatalf("expected structured install output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Secret: [redacted]") {
		t.Fatalf("expected redacted secret in command stdout, got: %s", rendered)
	}
	if strings.Contains(rendered, "supersecret") {
		t.Fatalf("expected full secret to stay hidden in command stdout, got: %s", rendered)
	}
	if strings.Contains(rendered, "tg://proxy?") {
		t.Fatalf("expected full proxy link to stay hidden in command stdout, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Proxy link: hidden (use `mtproxy link` to print full link)") {
		t.Fatalf("expected proxy link hint in command stdout, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "install lifecycle begin") {
		t.Fatalf("expected install begin log, got: %s", logText)
	}
	if !strings.Contains(logText, "install lifecycle finish") {
		t.Fatalf("expected install finish log, got: %s", logText)
	}
	if strings.Contains(logText, "tg://proxy?") {
		t.Fatalf("expected proxy link to stay out of logs, got: %s", logText)
	}
	if strings.Contains(logText, "supersecret") {
		t.Fatalf("expected secret to stay out of logs, got: %s", logText)
	}
}
