package output

import (
	"strings"
	"testing"

	"mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
)

func TestRenderTelemtStatusKeepsProxyLinkRedacted(t *testing.T) {
	t.Parallel()

	summary := telemt.StatusSummary{
		Provider:        runtime.ProviderTelemt,
		InstallDir:      "/opt/mtproxy-installer",
		RuntimeStatus:   telemt.RuntimeStatusHealthy,
		ComposeStatus:   telemt.ComposeStatusRunning,
		ComposeReason:   "compose_ps_running",
		HealthStatus:    telemt.HealthStatusHealthy,
		HealthReason:    "health_ok",
		LinkStatus:      telemt.LinkStatusAvailable,
		LinkReason:      "link_ok",
		ProxyLink:       "tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
		ControlEndpoint: "http://127.0.0.1:9091",
	}

	rendered := RenderTelemtStatus(summary)
	if strings.Contains(rendered, "tg://proxy?") {
		t.Fatalf("expected status renderer to keep proxy link redacted, got: %s", rendered)
	}
	if !strings.Contains(rendered, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction marker in status output, got: %s", rendered)
	}
}

func TestRenderTelemtLinkReturnsFullLinkWhenAvailable(t *testing.T) {
	t.Parallel()

	link := "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"
	summary := telemt.StatusSummary{
		Provider:   runtime.ProviderTelemt,
		LinkStatus: telemt.LinkStatusAvailable,
		ProxyLink:  link,
	}

	rendered := RenderTelemtLink(summary)
	if rendered != link {
		t.Fatalf("expected full proxy link in link renderer, got: %s", rendered)
	}
}

func TestRenderUnsupportedStatusIncludesProviderWarning(t *testing.T) {
	t.Parallel()

	rendered := RenderUnsupportedStatus(UnsupportedProviderSummary{
		InstallDir:     "/opt/mtproxy-installer",
		Provider:       "mtg",
		ComposeChecked: true,
		ComposeStatus:  telemt.ComposeStatusRunning,
		ComposeReason:  "compose_ps_running",
	})

	if !strings.Contains(rendered, `provider "mtg"`) {
		t.Fatalf("expected provider warning in unsupported status output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "unsupported_for_provider") {
		t.Fatalf("expected unsupported markers in output, got: %s", rendered)
	}
}

func TestRenderTelemtLinkWithoutUsableLinkIsActionable(t *testing.T) {
	t.Parallel()

	rendered := RenderTelemtLink(telemt.StatusSummary{
		Provider:      runtime.ProviderTelemt,
		RuntimeStatus: telemt.RuntimeStatusLinkUnavailable,
		ComposeStatus: telemt.ComposeStatusRunning,
		HealthStatus:  telemt.HealthStatusHealthy,
		LinkStatus:    telemt.LinkStatusUnavailable,
		LinkReason:    "users_without_tls_links",
	})

	if !strings.Contains(rendered, "Proxy link unavailable for telemt runtime.") {
		t.Fatalf("expected actionable unavailable message, got: %s", rendered)
	}
	if strings.Contains(rendered, "tg://proxy?") {
		t.Fatalf("expected no full proxy link in unavailable output, got: %s", rendered)
	}
}

func TestRenderUnsupportedLinkIncludesComposeAndRuntimeHints(t *testing.T) {
	t.Parallel()

	rendered := RenderUnsupportedLink(UnsupportedProviderSummary{
		InstallDir:          "/opt/mtproxy-installer",
		Provider:            "official",
		ComposeChecked:      true,
		ComposeStatus:       telemt.ComposeStatusUnknown,
		ComposeReason:       "compose_ps_failed",
		ComposeStderr:       "external command failed: permission denied",
		RuntimeResolveError: "provider unsupported",
	})

	if !strings.Contains(rendered, `provider "official"`) {
		t.Fatalf("expected provider hint in unsupported link output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Compose: unknown (compose_ps_failed)") {
		t.Fatalf("expected compose diagnostics in unsupported link output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Compose diagnostics: external command failed: permission denied") {
		t.Fatalf("expected compose stderr diagnostics in unsupported link output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Runtime diagnostics: provider unsupported") {
		t.Fatalf("expected runtime diagnostics in unsupported link output, got: %s", rendered)
	}
}

func TestRenderUnsupportedStatusComposeInitFailureShowsError(t *testing.T) {
	t.Parallel()

	rendered := RenderUnsupportedStatus(UnsupportedProviderSummary{
		InstallDir:       "/opt/mtproxy-installer",
		Provider:         "mtg",
		ComposeChecked:   false,
		ComposeStatus:    telemt.ComposeStatusUnknown,
		ComposeReason:    "compose_adapter_init_failed",
		ComposeInitError: "compose binary not found",
	})

	if !strings.Contains(rendered, "Compose: error (compose_adapter_init_failed)") {
		t.Fatalf("expected compose error marker in unsupported status output, got: %s", rendered)
	}
	if strings.Contains(rendered, "Compose: skipped") {
		t.Fatalf("expected compose init failure to avoid skipped marker, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Compose diagnostics: compose binary not found") {
		t.Fatalf("expected compose diagnostics in unsupported status output, got: %s", rendered)
	}
}
