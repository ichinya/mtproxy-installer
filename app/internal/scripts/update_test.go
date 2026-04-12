package scripts

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
)

func TestParseUpdateLifecycleClassifiesAlreadyUpToDate(t *testing.T) {
	t.Parallel()

	summary := ParseUpdateLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\nImage is already up to date: ghcr.io/example/telemt@sha256:abc\ntelemt update complete\nProvider: telemt\nSource: ghcr.io/example/telemt:stable\nActive: ghcr.io/example/telemt@sha256:abc\nHealth: curl http://127.0.0.1:9091/v1/health\n",
	}, nil)

	if summary.Status != UpdateStatusAlreadyUpToDate {
		t.Fatalf("expected already_up_to_date status, got %s", summary.Status)
	}
	if summary.RollbackTriggered {
		t.Fatalf("expected rollback_triggered=false")
	}
	if summary.ActiveImage != "ghcr.io/example/telemt@sha256:abc" {
		t.Fatalf("unexpected active image: %q", summary.ActiveImage)
	}
}

func TestParseUpdateLifecycleClassifiesUpdated(t *testing.T) {
	t.Parallel()

	summary := ParseUpdateLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\ntelemt update complete\nProvider: telemt\nSource: ghcr.io/example/telemt:stable\nActive: ghcr.io/example/telemt@sha256:def\n",
	}, nil)

	if summary.Status != UpdateStatusUpdated {
		t.Fatalf("expected updated status, got %s", summary.Status)
	}
	if summary.RollbackTriggered {
		t.Fatalf("expected rollback_triggered=false")
	}
	if summary.ActiveImage != "ghcr.io/example/telemt@sha256:def" {
		t.Fatalf("unexpected active image: %q", summary.ActiveImage)
	}
}

func TestParseUpdateLifecycleClassifiesRolledBack(t *testing.T) {
	t.Parallel()

	summary := ParseUpdateLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\nPrepared rollback image: mtproxy-installer/telemt-backup:20260411\nRolling back telemt to mtproxy-installer/telemt-backup:20260411\n",
		Stderr: "Error: Update failed while restarting the provider. Previous image restored.\n",
	}, errors.New("external command failed"))

	if summary.Status != UpdateStatusRolledBack {
		t.Fatalf("expected rolled_back status, got %s", summary.Status)
	}
	if !summary.RollbackTriggered {
		t.Fatalf("expected rollback_triggered=true")
	}
	if summary.PreparedRollbackImage != "mtproxy-installer/telemt-backup:20260411" {
		t.Fatalf("unexpected prepared rollback image: %q", summary.PreparedRollbackImage)
	}
}

func TestParseUpdateLifecycleTreatsUnconfirmedRollbackAsFailed(t *testing.T) {
	t.Parallel()

	summary := ParseUpdateLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\nPrepared rollback image: mtproxy-installer/telemt-backup:20260411\nRolling back telemt to mtproxy-installer/telemt-backup:20260411\n",
		Stderr: "Error: Rollback failed. Manual recovery required.\n",
	}, errors.New("external command failed"))

	if summary.Status != UpdateStatusFailed {
		t.Fatalf("expected failed status for unconfirmed rollback, got %s", summary.Status)
	}
	if !summary.RollbackTriggered {
		t.Fatalf("expected rollback_triggered=true")
	}
	if len(summary.ParseDiagnostics) == 0 {
		t.Fatalf("expected parse diagnostics for unconfirmed rollback")
	}
}

func TestParseUpdateLifecycleClassifiesFailed(t *testing.T) {
	t.Parallel()

	summary := ParseUpdateLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\n",
	}, errors.New("external command failed"))

	if summary.Status != UpdateStatusFailed {
		t.Fatalf("expected failed status, got %s", summary.Status)
	}
	if summary.RollbackTriggered {
		t.Fatalf("expected rollback_triggered=false")
	}
}

func TestParseUpdateLifecycleTreatsMissingCompletionMarkerAsFailed(t *testing.T) {
	t.Parallel()

	summary := ParseUpdateLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nConfigured source: ghcr.io/example/telemt:stable\n",
	}, nil)

	if summary.Status != UpdateStatusFailed {
		t.Fatalf("expected failed status for missing completion marker, got %s", summary.Status)
	}
	if len(summary.ParseDiagnostics) == 0 {
		t.Fatalf("expected parse diagnostics for missing completion marker")
	}
}

func TestManagerUpdateReturnsErrorWhenLifecycleStatusIsFailed(t *testing.T) {
	repoRoot := t.TempDir()
	createScriptSetForRepoRootTest(t, repoRoot)
	updateScriptPath := filepath.Join(repoRoot, updateScriptName)
	updateScriptBody := "#!/usr/bin/env bash\nset -euo pipefail\necho \"Install dir: ${INSTALL_DIR}\"\necho \"Provider: telemt\"\necho \"Configured source: ghcr.io/example/telemt:stable\"\necho \"Source: ghcr.io/example/telemt:stable\"\necho \"Active: ghcr.io/example/telemt@sha256:abc\"\n"
	if err := os.WriteFile(updateScriptPath, []byte(updateScriptBody), 0o700); err != nil {
		t.Fatalf("failed to write update script: %v", err)
	}

	installDir := t.TempDir()
	providerDir := filepath.Join(installDir, "providers", "telemt")
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatalf("failed to create provider directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".env"), []byte("PROVIDER=telemt\nINSTALL_DIR="+installDir+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}
	composeBody := "services:\n  telemt:\n    volumes:\n      - ./providers/telemt/telemt.toml:/etc/telemt.toml:ro\n"
	if err := os.WriteFile(filepath.Join(installDir, "docker-compose.yml"), []byte(composeBody), 0o600); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "telemt.toml"), []byte("[general]\n"), 0o600); err != nil {
		t.Fatalf("failed to write provider config: %v", err)
	}

	bashPath, err := resolveTrustedBinaryPath("bash", "", trustedBashBinaryCandidates)
	if err != nil {
		t.Skipf("bash is unavailable in this environment: %v", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	manager, err := NewManager(ManagerOptions{
		Logger:   logger,
		RepoRoot: repoRoot,
		BashPath: bashPath,
	})
	if err != nil {
		t.Fatalf("failed to initialize manager: %v", err)
	}

	result, err := manager.Update(context.Background(), UpdateOptions{
		InstallDir:                installDir,
		AllowNonDefaultInstallDir: true,
	})
	if err == nil {
		t.Fatalf("expected update to fail when lifecycle status is failed")
	}
	if !strings.Contains(err.Error(), "parse failed") {
		t.Fatalf("expected parse failure error, got %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected script to exit successfully before parser failure, got exit code %d", result.ExitCode)
	}

	logText := logs.String()
	if !strings.Contains(logText, "update adapter failed") {
		t.Fatalf("expected update adapter failure log, got: %s", logText)
	}
	if strings.Contains(logText, "update adapter finish") {
		t.Fatalf("did not expect update adapter finish log for failed lifecycle summary, got: %s", logText)
	}
}
