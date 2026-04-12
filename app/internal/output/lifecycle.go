package output

import (
	"fmt"
	"strings"

	"mtproxy-installer/app/internal/scripts"
)

func RenderInstallLifecycle(summary scripts.InstallLifecycleSummary) string {
	lines := []string{
		"Install status: installed",
		fmt.Sprintf("Provider: %s", normalizeLifecycleValue(summary.Provider, "unknown")),
		fmt.Sprintf("Install dir: %s", normalizeLifecycleValue(summary.InstallDir, "unknown")),
		fmt.Sprintf("Public endpoint: %s", normalizeLifecycleValue(summary.PublicEndpoint, "n/a")),
		fmt.Sprintf("API endpoint: %s", normalizeLifecycleValue(summary.APIEndpoint, "n/a")),
		fmt.Sprintf("Config: %s", normalizeLifecycleValue(summary.ConfigPath, "n/a")),
	}

	if strings.TrimSpace(summary.Secret) != "" {
		lines = append(lines, "Secret: [redacted]")
	}
	if summary.ProxyLinkPresent {
		lines = append(lines, "Proxy link: hidden (use `mtproxy link` to print full link)")
	}
	if strings.TrimSpace(summary.LogsHint) != "" {
		lines = append(lines, fmt.Sprintf("Logs: %s", summary.LogsHint))
	}
	for _, hint := range summary.OperatorHints {
		lines = append(lines, hint)
	}
	lines = append(lines, fmt.Sprintf("Sensitive output present: %t", summary.SensitiveOutputPresent))
	if len(summary.ParseDiagnostics) > 0 {
		lines = append(lines, fmt.Sprintf("Parse diagnostics: %s", strings.Join(summary.ParseDiagnostics, "; ")))
	}

	return strings.Join(lines, "\n")
}

func RenderUpdateLifecycle(summary scripts.UpdateLifecycleSummary) string {
	lines := []string{
		fmt.Sprintf("Update status: %s", summary.Status),
		fmt.Sprintf("Provider: %s", normalizeLifecycleValue(summary.Provider, "unknown")),
		fmt.Sprintf("Install dir: %s", normalizeLifecycleValue(summary.InstallDir, "unknown")),
		fmt.Sprintf("Source: %s", normalizeLifecycleValue(summary.SourceRef, "n/a")),
		fmt.Sprintf("Active image: %s", normalizeLifecycleValue(summary.ActiveImage, "n/a")),
		fmt.Sprintf("Rollback triggered: %t", summary.RollbackTriggered),
	}
	if strings.TrimSpace(summary.PreparedRollbackImage) != "" {
		lines = append(lines, fmt.Sprintf("Prepared rollback image: %s", summary.PreparedRollbackImage))
	}
	for _, hint := range summary.OperatorHints {
		lines = append(lines, hint)
	}
	if len(summary.ParseDiagnostics) > 0 {
		lines = append(lines, fmt.Sprintf("Parse diagnostics: %s", strings.Join(summary.ParseDiagnostics, "; ")))
	}
	return strings.Join(lines, "\n")
}

func normalizeLifecycleValue(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
