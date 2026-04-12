package cli

import (
	"errors"
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
)

func TestParseUpdateCommandArgsRejectsProviderFlag(t *testing.T) {
	_, err := parseUpdateCommandArgs([]string{"--provider", "telemt"})
	if err == nil {
		t.Fatalf("expected parse error for unsupported update flag")
	}
	if !strings.Contains(err.Error(), "--provider") {
		t.Fatalf("expected unsupported flag marker, got %v", err)
	}
}

func TestParseUpdateCommandArgsRejectsTrustBoundaryEnvWithoutAllowFlag(t *testing.T) {
	_, err := parseUpdateCommandArgs([]string{"--env", "DOCKER_HOST=tcp://docker:2375"})
	if err == nil {
		t.Fatalf("expected parse error for trust-boundary env override without allow flag")
	}
	if !strings.Contains(err.Error(), "--allow-trust-boundary-env") {
		t.Fatalf("expected allow flag hint, got %v", err)
	}
}

func TestRunUpdateRendersAlreadyUpToDateStatus(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("SUDO_UID", "")
	t.Setenv("SUDO_USER", "")

	stub := &lifecycleManagerStub{
		updateResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\nImage is already up to date: ghcr.io/example/telemt@sha256:abc\ntelemt update complete\nProvider: telemt\nSource: ghcr.io/example/telemt:stable\nActive: ghcr.io/example/telemt@sha256:abc\nHealth: curl http://127.0.0.1:9091/v1/health\n",
		},
	}
	withLifecycleManagerStub(t, stub)

	ctx, stdout, logs := newTestCommandContext(
		"update",
		"--install-dir", "/opt/mtproxy-installer",
		"--allow-trust-boundary-env",
		"--env", "DOCKER_HOST=tcp://docker:2375",
	)
	if err := runUpdate(ctx); err != nil {
		t.Fatalf("expected update success, got %v", err)
	}

	if len(stub.updateRequests) != 1 {
		t.Fatalf("expected one update request, got %d", len(stub.updateRequests))
	}
	if got := stub.updateRequests[0].ExtraEnv["DOCKER_HOST"]; got != "tcp://docker:2375" {
		t.Fatalf("unexpected env mapping: %q", got)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Update status: already_up_to_date") {
		t.Fatalf("expected already_up_to_date output, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "update lifecycle begin") {
		t.Fatalf("expected update begin log, got: %s", logText)
	}
	if !strings.Contains(logText, "update lifecycle finish") {
		t.Fatalf("expected update finish log, got: %s", logText)
	}
}

func TestRunUpdateReturnsErrorWhenRollbackTriggered(t *testing.T) {
	stub := &lifecycleManagerStub{
		updateResult: execadapter.Result{
			ExitCode:      1,
			Stdout:        "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\nPrepared rollback image: mtproxy-installer/telemt-backup:20260411\nRolling back telemt to mtproxy-installer/telemt-backup:20260411\n",
			StderrSummary: "Error: Update failed while restarting the provider. Previous image restored.",
		},
		updateErr: errors.New("external command failed"),
	}
	withLifecycleManagerStub(t, stub)

	ctx, stdout, logs := newTestCommandContext("update")
	err := runUpdate(ctx)
	if err == nil {
		t.Fatalf("expected rollback path to return error")
	}

	rendered := stdout.String()
	if strings.TrimSpace(rendered) != "" {
		t.Fatalf("expected no structured summary in error path, got: %s", rendered)
	}
	logText := logs.String()
	if !strings.Contains(logText, "update lifecycle failed") {
		t.Fatalf("expected error log for rollback path, got: %s", logText)
	}
	if !strings.Contains(logText, "rollback_triggered=true") {
		t.Fatalf("expected rollback marker in logs, got: %s", logText)
	}
}

func TestRunUpdateReturnsErrorWhenCompletionMarkerIsMissing(t *testing.T) {
	stub := &lifecycleManagerStub{
		updateResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\n",
		},
	}
	withLifecycleManagerStub(t, stub)

	ctx, stdout, logs := newTestCommandContext("update")
	err := runUpdate(ctx)
	if err == nil {
		t.Fatalf("expected parse failure when completion marker is missing")
	}

	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("expected no structured summary in parse-failure path, got: %s", stdout.String())
	}
	logText := logs.String()
	if !strings.Contains(logText, "update lifecycle failed") {
		t.Fatalf("expected error log for parse-failure path, got: %s", logText)
	}
	if !strings.Contains(logText, "parse failed") {
		t.Fatalf("expected parse failure details in logs, got: %s", logText)
	}
}
