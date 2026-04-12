package scripts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

const UninstallStrategyTelemtOnly = "telemt_only"

type UninstallOptions struct {
	InstallDir                string
	KeepData                  bool
	DetectedProvider          runtime.Provider
	AllowNonDefaultInstallDir bool
	ExtraEnv                  map[string]string
}

type UninstallCleanupStatus string

const (
	UninstallCleanupStatusCompleted                  UninstallCleanupStatus = "completed"
	UninstallCleanupStatusCompletedKeepData          UninstallCleanupStatus = "completed_keep_data"
	UninstallCleanupStatusPartial                    UninstallCleanupStatus = "partial"
	UninstallCleanupStatusBlockedUnsupportedProvider UninstallCleanupStatus = "blocked_unsupported_provider"
	UninstallCleanupStatusBlockedProviderMismatch    UninstallCleanupStatus = "blocked_provider_mismatch"
	UninstallCleanupStatusBlockedAmbiguousProvider   UninstallCleanupStatus = "blocked_ambiguous_provider"
	UninstallCleanupStatusFailedPreflight            UninstallCleanupStatus = "failed_preflight"
	UninstallCleanupStatusFailed                     UninstallCleanupStatus = "failed"
	UninstallCleanupStatusUnknown                    UninstallCleanupStatus = "unknown"
)

const uninstallLifecycleFlagUnknown = "unknown"

type UninstallLifecycleSummary struct {
	Provider         string
	InstallDir       string
	KeepData         bool
	KeepDataDetected bool
	Strategy         string
	CleanupStatus    UninstallCleanupStatus
	DataRemoved      string
	ImageCleanup     string
	OperatorHints    []string
	ParseDiagnostics []string
}

func ResolveUninstallProviderContract(runtimeProvider runtime.Provider, providerHint runtime.Provider) (runtime.Provider, error) {
	normalizedRuntimeProvider, err := normalizeUninstallContractProvider(runtimeProvider, false)
	if err != nil {
		return "", err
	}
	normalizedProviderHint, err := normalizeUninstallContractProvider(providerHint, true)
	if err != nil {
		return "", err
	}

	if normalizedProviderHint != "" && normalizedProviderHint != normalizedRuntimeProvider {
		return "", fmt.Errorf(
			"uninstall strategy %q rejected provider mismatch: hint=%q runtime=%q",
			UninstallStrategyTelemtOnly,
			normalizedProviderHint,
			normalizedRuntimeProvider,
		)
	}

	if normalizedRuntimeProvider != runtime.ProviderTelemt {
		return "", fmt.Errorf(
			"uninstall strategy %q supports provider %q only; detected provider %q",
			UninstallStrategyTelemtOnly,
			runtime.ProviderTelemt,
			normalizedRuntimeProvider,
		)
	}

	return normalizedRuntimeProvider, nil
}

func (m *Manager) Uninstall(ctx context.Context, options UninstallOptions) (execadapter.Result, error) {
	strategyMode := UninstallStrategyTelemtOnly
	installDir := installDirForLog(options.InstallDir)
	providerHint := strings.TrimSpace(string(options.DetectedProvider))

	m.logger.Warn(
		"uninstall adapter destructive intent received",
		"strategy", strategyMode,
		"provider_hint", normalizeLogValue(providerHint, "auto"),
		"install_dir", installDir,
		"keep_data", options.KeepData,
		"allow_non_default_install_dir", options.AllowNonDefaultInstallDir,
		"preflight_status", "pending",
	)

	runtimeState, preflightErr := m.preflightRuntimeInstallDir(options.InstallDir)
	if preflightErr != nil {
		m.logUninstallPreflightError(
			"runtime_preflight_failed",
			installDir,
			providerHint,
			options.KeepData,
			preflightErr,
		)
		return execadapter.Result{}, preflightErr
	}
	if err := enforceInstallDirDestructivePolicy("uninstall", runtimeState.Paths.InstallDir, options.AllowNonDefaultInstallDir); err != nil {
		m.logUninstallPreflightError(
			"install_dir_policy_failed",
			runtimeState.Paths.InstallDir,
			providerHint,
			options.KeepData,
			err,
		)
		return execadapter.Result{}, err
	}
	runtimeState, preflightErr = m.recheckRuntimeStateAtExecution(runtimeState)
	if preflightErr != nil {
		m.logUninstallPreflightError(
			"runtime_execution_recheck_failed",
			runtimeState.Paths.InstallDir,
			providerHint,
			options.KeepData,
			preflightErr,
		)
		return execadapter.Result{}, preflightErr
	}

	resolvedProvider, contractErr := ResolveUninstallProviderContract(runtimeState.Provider.Name, options.DetectedProvider)
	if contractErr != nil {
		m.logger.Error(
			"uninstall adapter provider contract rejected runtime",
			"strategy", strategyMode,
			"provider_hint", normalizeLogValue(providerHint, "auto"),
			"runtime_provider", runtimeState.Provider.Name,
			"install_dir", runtimeState.Paths.InstallDir,
			"keep_data", options.KeepData,
			"preflight_status", "rejected",
			"error", execadapter.RedactText(contractErr.Error()),
		)
		return execadapter.Result{}, contractErr
	}

	scriptPath, err := m.resolveUninstallScriptPath()
	if err != nil {
		return execadapter.Result{}, err
	}

	envOverrides := copyEnv(options.ExtraEnv)
	envOverrides["INSTALL_DIR"] = runtimeState.Paths.InstallDir
	if options.KeepData {
		envOverrides["KEEP_DATA"] = "true"
	} else {
		envOverrides["KEEP_DATA"] = "false"
	}
	envOverrides, err = sanitizeEnvOverrides(
		"uninstall",
		envOverrides,
		uninstallEnvOverrideAllowlist,
		envOverrideTrustPolicy{
			AllowTrustBoundaryEnv: false,
			PrivilegedContext:     m.privilegedExecutionScope,
		},
	)
	if err != nil {
		return execadapter.Result{}, err
	}

	m.logger.Warn(
		"uninstall adapter destructive execution requested",
		"strategy", strategyMode,
		"script_path", scriptPath,
		"provider_hint", normalizeLogValue(providerHint, string(resolvedProvider)),
		"runtime_provider", runtimeState.Provider.Name,
		"install_dir", runtimeState.Paths.InstallDir,
		"keep_data", options.KeepData,
		"allow_non_default_install_dir", options.AllowNonDefaultInstallDir,
		"preflight_status", "accepted",
	)
	m.logger.Debug(
		"uninstall adapter request assembled",
		"strategy", strategyMode,
		"script_path", scriptPath,
		"working_dir", m.repoRoot,
		"env_override_keys", sortedKeys(envOverrides),
		"env_overrides", execadapter.RedactEnvSnapshot(envOverrides),
	)
	m.logger.Info(
		"uninstall adapter start",
		"strategy", strategyMode,
		"script_path", scriptPath,
		"provider", resolvedProvider,
		"install_dir", runtimeState.Paths.InstallDir,
		"keep_data", options.KeepData,
	)

	runtimeState, preflightErr = m.recheckRuntimeStateAtExecution(runtimeState)
	if preflightErr != nil {
		return execadapter.Result{}, preflightErr
	}
	if err := enforceInstallDirDestructivePolicy("uninstall", runtimeState.Paths.InstallDir, options.AllowNonDefaultInstallDir); err != nil {
		return execadapter.Result{}, err
	}
	envOverrides["INSTALL_DIR"] = runtimeState.Paths.InstallDir

	_, contractErr = ResolveUninstallProviderContract(runtimeState.Provider.Name, resolvedProvider)
	if contractErr != nil {
		m.logger.Error(
			"uninstall adapter provider contract changed during execution preflight",
			"strategy", strategyMode,
			"provider_hint", resolvedProvider,
			"runtime_provider", runtimeState.Provider.Name,
			"install_dir", runtimeState.Paths.InstallDir,
			"keep_data", options.KeepData,
			"preflight_status", "rejected",
			"error", execadapter.RedactText(contractErr.Error()),
		)
		return execadapter.Result{}, contractErr
	}

	result, runErr := m.runner.Run(ctx, execadapter.Request{
		Command:          m.bashPath,
		Args:             []string{scriptPath},
		WorkingDir:       m.repoRoot,
		EnvOverrides:     envOverrides,
		InheritParentEnv: false,
		AllowedEnvKeys:   sortedKeys(envOverrides),
		UseSafePath:      true,
	})

	summary := ParseUninstallLifecycle(result, runErr)
	m.logger.Debug(
		"uninstall adapter parsed lifecycle summary",
		"strategy", normalizeLogValue(summary.Strategy, strategyMode),
		"provider", normalizeLogValue(summary.Provider, string(resolvedProvider)),
		"install_dir", normalizeLogValue(summary.InstallDir, runtimeState.Paths.InstallDir),
		"keep_data", summary.KeepData,
		"keep_data_detected", summary.KeepDataDetected,
		"cleanup_status", summary.CleanupStatus,
		"data_removed", summary.DataRemoved,
		"image_cleanup", summary.ImageCleanup,
		"operator_hints", summary.OperatorHints,
		"parse_diagnostics", summary.ParseDiagnostics,
	)

	reportedErr := runErr
	if reportedErr == nil && !isUninstallLifecycleSuccess(summary.CleanupStatus) {
		switch summary.CleanupStatus {
		case UninstallCleanupStatusPartial:
			reportedErr = fmt.Errorf("uninstall lifecycle reported partial cleanup")
		case UninstallCleanupStatusBlockedUnsupportedProvider,
			UninstallCleanupStatusBlockedProviderMismatch,
			UninstallCleanupStatusBlockedAmbiguousProvider:
			reportedErr = fmt.Errorf("uninstall lifecycle rejected runtime with cleanup status %q", summary.CleanupStatus)
		case UninstallCleanupStatusFailedPreflight, UninstallCleanupStatusFailed:
			reportedErr = fmt.Errorf("uninstall lifecycle failed with cleanup status %q", summary.CleanupStatus)
		default:
			reportedErr = fmt.Errorf("uninstall lifecycle outcome is unknown")
		}
	}
	if reportedErr == nil && len(summary.ParseDiagnostics) > 0 {
		reportedErr = fmt.Errorf("uninstall lifecycle parse diagnostics present: %s", strings.Join(summary.ParseDiagnostics, "; "))
	}

	if reportedErr != nil {
		m.logger.Error(
			"uninstall adapter failed",
			"strategy", normalizeLogValue(summary.Strategy, strategyMode),
			"provider", normalizeLogValue(summary.Provider, string(resolvedProvider)),
			"install_dir", normalizeLogValue(summary.InstallDir, runtimeState.Paths.InstallDir),
			"keep_data", summary.KeepData,
			"cleanup_status", summary.CleanupStatus,
			"data_removed", summary.DataRemoved,
			"image_cleanup", summary.ImageCleanup,
			"operator_hints", summary.OperatorHints,
			"parse_diagnostics", summary.ParseDiagnostics,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", execadapter.RedactText(reportedErr.Error()),
		)
		return result, reportedErr
	}

	m.logger.Info(
		"uninstall adapter finish",
		"strategy", normalizeLogValue(summary.Strategy, strategyMode),
		"provider", normalizeLogValue(summary.Provider, string(resolvedProvider)),
		"install_dir", normalizeLogValue(summary.InstallDir, runtimeState.Paths.InstallDir),
		"keep_data", summary.KeepData,
		"cleanup_status", summary.CleanupStatus,
		"data_removed", summary.DataRemoved,
		"image_cleanup", summary.ImageCleanup,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
		"stderr_summary", result.StderrSummary,
	)

	return result, nil
}

func ParseUninstallLifecycle(result execadapter.Result, runErr error) UninstallLifecycleSummary {
	summary := UninstallLifecycleSummary{
		Strategy:         UninstallStrategyTelemtOnly,
		CleanupStatus:    UninstallCleanupStatusUnknown,
		DataRemoved:      uninstallLifecycleFlagUnknown,
		ImageCleanup:     uninstallLifecycleFlagUnknown,
		OperatorHints:    make([]string, 0, 4),
		ParseDiagnostics: make([]string, 0, 4),
	}

	lines := append(splitLifecycleLines(result.Stdout), splitLifecycleLines(result.Stderr)...)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "Install dir:"):
			summary.InstallDir = parseLifecycleField(line, "Install dir:")
		case strings.HasPrefix(line, "Provider:"):
			summary.Provider = parseLifecycleField(line, "Provider:")
		case strings.HasPrefix(line, "Strategy:"):
			summary.Strategy = parseLifecycleField(line, "Strategy:")
		case strings.HasPrefix(line, "Keep data:"):
			parsedKeepData, ok := parseLifecycleBool(parseLifecycleField(line, "Keep data:"))
			summary.KeepData = parsedKeepData
			summary.KeepDataDetected = ok
		case strings.HasPrefix(line, "Cleanup status:"):
			summary.CleanupStatus = parseUninstallCleanupStatus(parseLifecycleField(line, "Cleanup status:"))
		case strings.HasPrefix(line, "Data removed:"):
			summary.DataRemoved = normalizeUninstallLifecycleValue(parseLifecycleField(line, "Data removed:"), uninstallLifecycleFlagUnknown)
		case strings.HasPrefix(line, "Image cleanup:"):
			summary.ImageCleanup = normalizeUninstallLifecycleValue(parseLifecycleField(line, "Image cleanup:"), uninstallLifecycleFlagUnknown)
		case strings.HasPrefix(line, "Hint:"):
			appendUniqueText(&summary.OperatorHints, line)
		case strings.HasPrefix(line, "Outcome:"):
			appendUniqueText(&summary.OperatorHints, line)
		case strings.HasPrefix(line, "WARN:"):
			appendUniqueText(&summary.OperatorHints, line)
		case strings.HasPrefix(line, "ERROR:"):
			appendUniqueText(&summary.OperatorHints, line)
		case strings.HasPrefix(line, "[FIX]"):
			appendUniqueText(&summary.OperatorHints, line)
		}
	}

	if summary.CleanupStatus == UninstallCleanupStatusUnknown {
		if runErr != nil {
			summary.CleanupStatus = UninstallCleanupStatusFailed
		} else {
			appendUniqueText(&summary.ParseDiagnostics, "cleanup status marker is missing in uninstall output")
		}
	}
	if strings.TrimSpace(summary.Provider) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "provider marker is missing in uninstall output")
	}
	if strings.TrimSpace(summary.InstallDir) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "install dir marker is missing in uninstall output")
	}
	if strings.TrimSpace(summary.Strategy) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "strategy marker is missing in uninstall output")
	}
	if !summary.KeepDataDetected {
		appendUniqueText(&summary.ParseDiagnostics, "keep data marker is missing or invalid in uninstall output")
	}
	if normalizeUninstallLifecycleValue(summary.DataRemoved, uninstallLifecycleFlagUnknown) == uninstallLifecycleFlagUnknown {
		appendUniqueText(&summary.ParseDiagnostics, "data removed marker is missing in uninstall output")
	}
	if normalizeUninstallLifecycleValue(summary.ImageCleanup, uninstallLifecycleFlagUnknown) == uninstallLifecycleFlagUnknown {
		appendUniqueText(&summary.ParseDiagnostics, "image cleanup marker is missing in uninstall output")
	}
	if runErr != nil && isUninstallLifecycleSuccess(summary.CleanupStatus) {
		appendUniqueText(&summary.ParseDiagnostics, "command failed but cleanup status reports success")
	}
	if summary.CleanupStatus == UninstallCleanupStatusPartial {
		appendUniqueText(&summary.ParseDiagnostics, "cleanup status reports partial removal")
	}

	return summary
}

func parseUninstallCleanupStatus(raw string) UninstallCleanupStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch UninstallCleanupStatus(normalized) {
	case UninstallCleanupStatusCompleted,
		UninstallCleanupStatusCompletedKeepData,
		UninstallCleanupStatusPartial,
		UninstallCleanupStatusBlockedUnsupportedProvider,
		UninstallCleanupStatusBlockedProviderMismatch,
		UninstallCleanupStatusBlockedAmbiguousProvider,
		UninstallCleanupStatusFailedPreflight,
		UninstallCleanupStatusFailed:
		return UninstallCleanupStatus(normalized)
	default:
		return UninstallCleanupStatusUnknown
	}
}

func parseLifecycleBool(raw string) (bool, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "true", "1", "yes":
		return true, true
	case "false", "0", "no":
		return false, true
	default:
		return false, false
	}
}

func normalizeUninstallLifecycleValue(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func isUninstallLifecycleSuccess(status UninstallCleanupStatus) bool {
	return status == UninstallCleanupStatusCompleted || status == UninstallCleanupStatusCompletedKeepData
}

func normalizeUninstallContractProvider(provider runtime.Provider, allowEmpty bool) (runtime.Provider, error) {
	normalized := strings.ToLower(strings.TrimSpace(string(provider)))
	if normalized == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("uninstall strategy %q requires detected runtime provider", UninstallStrategyTelemtOnly)
	}

	switch runtime.Provider(normalized) {
	case runtime.ProviderTelemt, runtime.ProviderMTG, runtime.ProviderOfficial:
		return runtime.Provider(normalized), nil
	default:
		return "", fmt.Errorf(
			"uninstall strategy %q received unsupported provider value %q",
			UninstallStrategyTelemtOnly,
			provider,
		)
	}
}

func (m *Manager) logUninstallPreflightError(
	stage string,
	installDir string,
	providerHint string,
	keepData bool,
	preflightErr error,
) {
	runtimeCode := ""
	var runtimeErr *runtime.RuntimeError
	if errors.As(preflightErr, &runtimeErr) {
		runtimeCode = string(runtimeErr.Code)
	}

	m.logger.Error(
		"uninstall adapter preflight failed",
		"stage", stage,
		"strategy", UninstallStrategyTelemtOnly,
		"provider_hint", normalizeLogValue(providerHint, "auto"),
		"install_dir", installDir,
		"keep_data", keepData,
		"runtime_error_code", normalizeLogValue(runtimeCode, "n/a"),
		"error", execadapter.RedactText(preflightErr.Error()),
	)
}
