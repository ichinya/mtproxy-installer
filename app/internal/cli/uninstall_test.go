package cli

import (
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

func TestParseUninstallCommandArgsRejectsProviderFlag(t *testing.T) {
	_, err := parseUninstallCommandArgs([]string{"--provider", "telemt"})
	if err == nil {
		t.Fatalf("expected parse error for unsupported --provider flag")
	}
	if !strings.Contains(err.Error(), "--provider") {
		t.Fatalf("expected provider flag in error, got %v", err)
	}
}

func TestRunUninstallRequiresYesConfirmation(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	stub := &lifecycleManagerStub{}
	withLifecycleManagerStub(t, stub)

	ctx, _, logs := newTestCommandContext("uninstall", "--install-dir", "/opt/mtproxy-installer")
	err := runUninstall(ctx)
	if err == nil {
		t.Fatalf("expected missing confirmation to fail")
	}
	if !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.uninstallRequests) != 0 {
		t.Fatalf("expected uninstall adapter to stay untouched without --yes")
	}

	logText := logs.String()
	if !strings.Contains(logText, "uninstall confirmation gate rejected") {
		t.Fatalf("expected confirmation warning log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Fatalf("expected WARN level for confirmation rejection, got: %s", logText)
	}
}

func TestRunUninstallRendersStructuredSummary(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}

	stub := &lifecycleManagerStub{
		uninstallResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nStrategy: telemt_only\nProvider: telemt\nKeep data: true\nCleanup status: completed_keep_data\nData removed: false\nImage cleanup: removed\nOutcome: Telemt runtime removed; install directory preserved\n",
		},
	}
	withLifecycleManagerStub(t, stub)

	ctx, stdout, logs := newTestCommandContext("uninstall", "--yes", "--keep-data", "--install-dir", "/opt/mtproxy-installer")
	if err := runUninstall(ctx); err != nil {
		t.Fatalf("expected uninstall success, got %v", err)
	}

	if len(stub.uninstallRequests) != 1 {
		t.Fatalf("expected one uninstall request, got %d", len(stub.uninstallRequests))
	}
	if !stub.uninstallRequests[0].KeepData {
		t.Fatalf("expected keep_data=true in uninstall request")
	}
	if stub.uninstallRequests[0].DetectedProvider != runtime.ProviderTelemt {
		t.Fatalf("unexpected detected provider: %q", stub.uninstallRequests[0].DetectedProvider)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Uninstall status: completed_keep_data") {
		t.Fatalf("expected structured uninstall status, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Strategy: telemt_only") {
		t.Fatalf("expected strategy marker in output, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "uninstall lifecycle begin") {
		t.Fatalf("expected uninstall begin log, got: %s", logText)
	}
	if !strings.Contains(logText, "uninstall lifecycle finish") {
		t.Fatalf("expected uninstall finish log, got: %s", logText)
	}
}

func TestRunUninstallRejectsUnsupportedProviderBeforeAdapter(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}

	stub := &lifecycleManagerStub{}
	withLifecycleManagerStub(t, stub)

	ctx, _, logs := newTestCommandContext("uninstall", "--yes")
	err := runUninstall(ctx)
	if err == nil {
		t.Fatalf("expected unsupported provider to fail")
	}
	if !strings.Contains(err.Error(), "supports provider \"telemt\" only") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.uninstallRequests) != 0 {
		t.Fatalf("expected adapter not to run for unsupported provider")
	}

	logText := logs.String()
	if !strings.Contains(logText, "uninstall provider contract rejected runtime") {
		t.Fatalf("expected provider rejection log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR level for unsupported provider, got: %s", logText)
	}
}

func TestRunUninstallFailsOnPartialCleanupSummary(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}

	stub := &lifecycleManagerStub{
		uninstallResult: execadapter.Result{
			Stdout: "Install dir: /opt/mtproxy-installer\nStrategy: telemt_only\nProvider: telemt\nKeep data: false\nCleanup status: partial\nData removed: false\nImage cleanup: failed\n",
		},
	}
	withLifecycleManagerStub(t, stub)

	ctx, stdout, logs := newTestCommandContext("uninstall", "--yes")
	err := runUninstall(ctx)
	if err == nil {
		t.Fatalf("expected partial cleanup summary to fail")
	}
	if !strings.Contains(err.Error(), "parse diagnostics") {
		t.Fatalf("expected parse-diagnostics failure, got %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("expected no structured output on partial cleanup failure, got: %s", stdout.String())
	}

	logText := logs.String()
	if !strings.Contains(logText, "uninstall lifecycle failed") {
		t.Fatalf("expected uninstall failure log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR log level for partial cleanup, got: %s", logText)
	}
}
