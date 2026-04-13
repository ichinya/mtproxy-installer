package scripts

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

func TestResolveUninstallProviderContractAcceptsTelemt(t *testing.T) {
	provider, err := ResolveUninstallProviderContract(runtime.ProviderTelemt, "")
	if err != nil {
		t.Fatalf("expected telemt provider to pass contract, got %v", err)
	}
	if provider != runtime.ProviderTelemt {
		t.Fatalf("unexpected provider %q", provider)
	}
}

func TestResolveUninstallProviderContractRejectsUnsupportedProvider(t *testing.T) {
	_, err := ResolveUninstallProviderContract(runtime.ProviderMTG, runtime.ProviderMTG)
	if err == nil {
		t.Fatalf("expected mtg provider to be rejected")
	}
	if !strings.Contains(err.Error(), "supports provider \"telemt\" only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveUninstallProviderContractRejectsProviderMismatch(t *testing.T) {
	_, err := ResolveUninstallProviderContract(runtime.ProviderTelemt, runtime.ProviderMTG)
	if err == nil {
		t.Fatalf("expected provider mismatch to be rejected")
	}
	if !strings.Contains(err.Error(), "provider mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseUninstallLifecycleParsesStructuredMarkers(t *testing.T) {
	t.Parallel()

	summary := ParseUninstallLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nStrategy: telemt_only\nProvider: telemt\nKeep data: false\nWARN: Destructive action requested\nCleanup status: completed\nData removed: true\nImage cleanup: removed\nOutcome: Telemt runtime removed\n",
	}, nil)

	if summary.CleanupStatus != UninstallCleanupStatusCompleted {
		t.Fatalf("expected completed status, got %q", summary.CleanupStatus)
	}
	if summary.Provider != "telemt" {
		t.Fatalf("unexpected provider %q", summary.Provider)
	}
	if summary.InstallDir != "/opt/mtproxy-installer" {
		t.Fatalf("unexpected install dir %q", summary.InstallDir)
	}
	if summary.KeepData {
		t.Fatalf("expected keep_data=false")
	}
	if !summary.KeepDataDetected {
		t.Fatalf("expected keep_data marker to be detected")
	}
	if summary.DataRemoved != "true" {
		t.Fatalf("unexpected data removed marker %q", summary.DataRemoved)
	}
	if summary.ImageCleanup != "removed" {
		t.Fatalf("unexpected image cleanup marker %q", summary.ImageCleanup)
	}
	if len(summary.ParseDiagnostics) != 0 {
		t.Fatalf("expected no parse diagnostics, got %v", summary.ParseDiagnostics)
	}
}

func TestParseUninstallLifecycleReportsPartialCleanup(t *testing.T) {
	t.Parallel()

	summary := ParseUninstallLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nStrategy: telemt_only\nProvider: telemt\nKeep data: false\nCleanup status: partial\nData removed: false\nImage cleanup: failed\n",
	}, nil)

	if summary.CleanupStatus != UninstallCleanupStatusPartial {
		t.Fatalf("expected partial status, got %q", summary.CleanupStatus)
	}
	if len(summary.ParseDiagnostics) == 0 {
		t.Fatalf("expected parse diagnostics for partial cleanup")
	}
}

func TestParseUninstallLifecycleMarksFailureWhenCommandErrorsWithoutCleanupMarker(t *testing.T) {
	t.Parallel()

	summary := ParseUninstallLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nStrategy: telemt_only\nProvider: telemt\nKeep data: true\nData removed: false\nImage cleanup: skipped\n",
	}, errors.New("external command failed"))

	if summary.CleanupStatus != UninstallCleanupStatusFailed {
		t.Fatalf("expected failed status when command errors without marker, got %q", summary.CleanupStatus)
	}
	if len(summary.ParseDiagnostics) != 0 {
		t.Fatalf("expected no parse diagnostics when command error drives failed status, got %v", summary.ParseDiagnostics)
	}
}

func TestParseUninstallLifecycleReportsMissingMarkers(t *testing.T) {
	t.Parallel()

	summary := ParseUninstallLifecycle(execadapter.Result{Stdout: "Uninstall start\n"}, nil)
	if summary.CleanupStatus != UninstallCleanupStatusUnknown {
		t.Fatalf("expected unknown status, got %q", summary.CleanupStatus)
	}
	if len(summary.ParseDiagnostics) == 0 {
		t.Fatalf("expected diagnostics for missing markers")
	}
}

func TestResolveUninstallScriptPathUsesManagerRootOutsidePrivilegedMode(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	createScriptSetForUninstallRepoRootTest(t, repoRoot)

	manager := &Manager{
		logger:                   slog.New(slog.NewTextHandler(io.Discard, nil)),
		repoRoot:                 repoRoot,
		privilegedExecutionScope: "",
	}

	scriptPath, err := manager.resolveUninstallScriptPath()
	if err != nil {
		t.Fatalf("expected uninstall script path to resolve, got %v", err)
	}
	expected := filepath.Join(repoRoot, uninstallScriptName)
	if canonicalPathKey(scriptPath) != canonicalPathKey(expected) {
		t.Fatalf("unexpected uninstall script path: %q", scriptPath)
	}
}

func TestResolveUninstallScriptPathUsesTrustedRootInPrivilegedMode(t *testing.T) {
	t.Parallel()

	if goRuntime.GOOS == "windows" {
		t.Skip("strict trusted-root permission checks are POSIX-specific in this test")
	}

	untrustedRoot := t.TempDir()
	createScriptSetForUninstallRepoRootTest(t, untrustedRoot)

	trustedRoot := t.TempDir()
	createScriptSetForUninstallRepoRootTest(t, trustedRoot)

	oldTrustedRoots := trustedLifecycleScriptRootCandidates
	trustedLifecycleScriptRootCandidates = []string{trustedRoot}
	t.Cleanup(func() {
		trustedLifecycleScriptRootCandidates = oldTrustedRoots
	})

	manager := &Manager{
		logger:                   slog.New(slog.NewTextHandler(io.Discard, nil)),
		repoRoot:                 untrustedRoot,
		privilegedExecutionScope: "uid=0",
	}

	scriptPath, err := manager.resolveUninstallScriptPath()
	if err != nil {
		t.Fatalf("expected privileged uninstall script path to resolve, got %v", err)
	}
	expected := filepath.Join(trustedRoot, uninstallScriptName)
	if canonicalPathKey(scriptPath) != canonicalPathKey(expected) {
		t.Fatalf("expected privileged uninstall script path from trusted root, got %q", scriptPath)
	}
}

func createScriptSetForUninstallRepoRootTest(t *testing.T, repoRoot string) {
	t.Helper()

	scriptPaths := []string{
		filepath.Join(repoRoot, installScriptName),
		filepath.Join(repoRoot, updateScriptName),
		filepath.Join(repoRoot, uninstallScriptName),
	}
	for _, scriptPath := range scriptPaths {
		if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\n"), 0o600); err != nil {
			t.Fatalf("failed to create lifecycle script %q: %v", scriptPath, err)
		}
	}
}
