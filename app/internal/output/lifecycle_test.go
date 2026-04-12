package output

import (
	"strings"
	"testing"

	"mtproxy-installer/app/internal/scripts"
)

func TestRenderInstallLifecycleRedactsSensitiveStdoutFields(t *testing.T) {
	t.Parallel()

	rendered := RenderInstallLifecycle(scripts.InstallLifecycleSummary{
		Provider:               "telemt",
		InstallDir:             "/opt/mtproxy-installer",
		PublicEndpoint:         "203.0.113.10:443",
		APIEndpoint:            "http://127.0.0.1:9091/v1/health",
		ConfigPath:             "/opt/mtproxy-installer/providers/telemt/telemt.toml",
		Secret:                 "abcdef",
		ProxyLink:              "tg://proxy?server=203.0.113.10&port=443&secret=abcdef",
		ProxyLinkPresent:       true,
		SensitiveOutputPresent: true,
		OperatorHints:          []string{"[FIX] Telegram voice calls are not guaranteed over MTProto proxy."},
	})

	if !strings.Contains(rendered, "Install status: installed") {
		t.Fatalf("expected install status line, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Secret: [redacted]") {
		t.Fatalf("expected redacted secret in command stdout rendering, got: %s", rendered)
	}
	if strings.Contains(rendered, "tg://proxy?") {
		t.Fatalf("expected proxy link to be hidden in command stdout rendering, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Proxy link: hidden (use `mtproxy link` to print full link)") {
		t.Fatalf("expected proxy link hint in command stdout rendering, got: %s", rendered)
	}
}

func TestRenderUpdateLifecycleIncludesRollbackStatus(t *testing.T) {
	t.Parallel()

	rendered := RenderUpdateLifecycle(scripts.UpdateLifecycleSummary{
		Status:                scripts.UpdateStatusRolledBack,
		Provider:              "telemt",
		InstallDir:            "/opt/mtproxy-installer",
		SourceRef:             "ghcr.io/example/telemt:stable",
		ActiveImage:           "ghcr.io/example/telemt@sha256:abc",
		RollbackTriggered:     true,
		PreparedRollbackImage: "mtproxy-installer/telemt-backup:20260411",
	})

	if !strings.Contains(rendered, "Update status: rolled_back") {
		t.Fatalf("expected rolled_back status line, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Rollback triggered: true") {
		t.Fatalf("expected rollback marker, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Prepared rollback image: mtproxy-installer/telemt-backup:20260411") {
		t.Fatalf("expected prepared rollback image line, got: %s", rendered)
	}
}

func TestRenderUninstallLifecycleIncludesStructuredFields(t *testing.T) {
	t.Parallel()

	rendered := RenderUninstallLifecycle(scripts.UninstallLifecycleSummary{
		Provider:         "telemt",
		InstallDir:       "/opt/mtproxy-installer",
		KeepData:         false,
		Strategy:         scripts.UninstallStrategyTelemtOnly,
		CleanupStatus:    scripts.UninstallCleanupStatusCompleted,
		DataRemoved:      "true",
		ImageCleanup:     "removed",
		OperatorHints:    []string{"WARN: Destructive action requested"},
		ParseDiagnostics: []string{},
	})

	if !strings.Contains(rendered, "Uninstall status: completed") {
		t.Fatalf("expected uninstall status line, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Strategy: telemt_only") {
		t.Fatalf("expected strategy line, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Data removed: true") {
		t.Fatalf("expected data removed line, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Image cleanup: removed") {
		t.Fatalf("expected image cleanup line, got: %s", rendered)
	}
}
