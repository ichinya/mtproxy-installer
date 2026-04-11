package output

import (
	"fmt"
	"strings"

	"mtproxy-installer/app/internal/provider/telemt"
)

type UnsupportedProviderSummary struct {
	InstallDir          string
	Provider            string
	ComposeChecked      bool
	ComposeStatus       telemt.ComposeStatus
	ComposeReason       string
	ComposeInitError    string
	ComposeStderr       string
	RuntimeResolveError string
}

func RenderTelemtStatus(summary telemt.StatusSummary) string {
	lines := []string{
		fmt.Sprintf("Runtime status: %s", summary.RuntimeStatus),
		fmt.Sprintf("Install dir: %s", normalizeEmpty(summary.InstallDir, "unknown")),
		fmt.Sprintf("Provider: %s", normalizeEmpty(string(summary.Provider), "unknown")),
		fmt.Sprintf("Compose: %s (%s)", summary.ComposeStatus, normalizeEmpty(summary.ComposeReason, "n/a")),
		fmt.Sprintf("Health: %s (%s)", summary.HealthStatus, normalizeEmpty(summary.HealthReason, "n/a")),
		fmt.Sprintf("Link: %s", renderTelemtLinkStatus(summary)),
		fmt.Sprintf("Control API: %s", normalizeEmpty(summary.ControlEndpoint, "n/a")),
	}
	if len(summary.DegradedReason) > 0 {
		lines = append(lines, fmt.Sprintf("Degraded reasons: %s", strings.Join(summary.DegradedReason, ", ")))
	}
	return strings.Join(lines, "\n")
}

func RenderTelemtLink(summary telemt.StatusSummary) string {
	if summary.LinkAvailable() {
		return summary.ProxyLink
	}

	lines := []string{
		"Proxy link unavailable for telemt runtime.",
		fmt.Sprintf("Runtime status: %s", summary.RuntimeStatus),
		fmt.Sprintf("Compose: %s", summary.ComposeStatus),
		fmt.Sprintf("Health: %s", summary.HealthStatus),
		fmt.Sprintf("Link reason: %s", normalizeEmpty(summary.LinkReason, "unknown")),
	}
	return strings.Join(lines, "\n")
}

func RenderUnsupportedStatus(summary UnsupportedProviderSummary) string {
	provider := normalizeEmpty(summary.Provider, "unknown")
	lines := []string{
		"Runtime status: partial",
		fmt.Sprintf("Install dir: %s", normalizeEmpty(summary.InstallDir, "unknown")),
		fmt.Sprintf("Provider: %s", provider),
		"Health: unsupported_for_provider",
		"Link: unsupported_for_provider",
	}

	if summary.ComposeChecked {
		lines = append(lines, fmt.Sprintf(
			"Compose: %s (%s)",
			summary.ComposeStatus,
			normalizeEmpty(summary.ComposeReason, "n/a"),
		))
	} else if summary.ComposeReason == "compose_adapter_init_failed" {
		lines = append(lines, fmt.Sprintf(
			"Compose: error (%s)",
			normalizeEmpty(summary.ComposeReason, "n/a"),
		))
	} else {
		lines = append(lines, "Compose: skipped (runtime provider fallback)")
	}

	lines = append(lines, fmt.Sprintf(
		"Warning: provider %q is not telemt; API health/link checks are unavailable for this runtime path.",
		provider,
	))
	if strings.TrimSpace(summary.ComposeStderr) != "" {
		lines = append(lines, fmt.Sprintf("Compose diagnostics: %s", summary.ComposeStderr))
	}
	if strings.TrimSpace(summary.ComposeInitError) != "" {
		lines = append(lines, fmt.Sprintf("Compose diagnostics: %s", summary.ComposeInitError))
	}
	if strings.TrimSpace(summary.RuntimeResolveError) != "" &&
		strings.TrimSpace(summary.RuntimeResolveError) != strings.TrimSpace(summary.ComposeInitError) {
		lines = append(lines, fmt.Sprintf("Runtime diagnostics: %s", summary.RuntimeResolveError))
	}

	return strings.Join(lines, "\n")
}

func RenderUnsupportedLink(summary UnsupportedProviderSummary) string {
	provider := normalizeEmpty(summary.Provider, "unknown")
	lines := []string{
		fmt.Sprintf("Proxy link is unavailable: provider %q is not supported by this command.", provider),
		"Supported runtime path: telemt.",
	}
	if summary.ComposeChecked {
		lines = append(lines, fmt.Sprintf(
			"Compose: %s (%s)",
			summary.ComposeStatus,
			normalizeEmpty(summary.ComposeReason, "n/a"),
		))
	} else if summary.ComposeReason == "compose_adapter_init_failed" {
		lines = append(lines, fmt.Sprintf(
			"Compose: error (%s)",
			normalizeEmpty(summary.ComposeReason, "n/a"),
		))
	} else {
		lines = append(lines, "Compose: skipped (runtime provider fallback)")
	}
	if strings.TrimSpace(summary.ComposeStderr) != "" {
		lines = append(lines, fmt.Sprintf("Compose diagnostics: %s", summary.ComposeStderr))
	}
	if strings.TrimSpace(summary.ComposeInitError) != "" {
		lines = append(lines, fmt.Sprintf("Compose diagnostics: %s", summary.ComposeInitError))
	}
	if strings.TrimSpace(summary.RuntimeResolveError) != "" &&
		strings.TrimSpace(summary.RuntimeResolveError) != strings.TrimSpace(summary.ComposeInitError) {
		lines = append(lines, fmt.Sprintf("Runtime diagnostics: %s", summary.RuntimeResolveError))
	}
	return strings.Join(lines, "\n")
}

func renderTelemtLinkStatus(summary telemt.StatusSummary) string {
	if summary.LinkAvailable() {
		return fmt.Sprintf("%s (%s)", summary.LinkStatus, summary.RedactedProxyLink())
	}
	return fmt.Sprintf("%s (%s)", summary.LinkStatus, normalizeEmpty(summary.LinkReason, "n/a"))
}

func normalizeEmpty(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
