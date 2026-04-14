package scripts

import (
	"context"
	"fmt"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
)

type UpdateOptions struct {
	// Runtime/image settings are sourced from the existing runtime .env contract.
	InstallDir                string
	AllowNonDefaultInstallDir bool
	ExtraEnv                  map[string]string
	AllowTrustBoundaryEnv     bool
}

type UpdateStatus string

const (
	UpdateStatusUpdated         UpdateStatus = "updated"
	UpdateStatusAlreadyUpToDate UpdateStatus = "already_up_to_date"
	UpdateStatusRolledBack      UpdateStatus = "rolled_back"
	UpdateStatusFailed          UpdateStatus = "failed"
)

type UpdateLifecycleSummary struct {
	Status                UpdateStatus
	Provider              string
	InstallDir            string
	SourceRef             string
	ActiveImage           string
	PreparedRollbackImage string
	RollbackTriggered     bool
	OperatorHints         []string
	ParseDiagnostics      []string
}

func (m *Manager) Update(ctx context.Context, options UpdateOptions) (execadapter.Result, error) {
	if err := ensureLifecycleHostOS("update"); err != nil {
		return execadapter.Result{}, err
	}

	runtimeState, preflightErr := m.preflightRuntimeInstallDir(options.InstallDir)
	if preflightErr != nil {
		return execadapter.Result{}, preflightErr
	}
	if err := enforceInstallDirDestructivePolicy("update", runtimeState.Paths.InstallDir, options.AllowNonDefaultInstallDir); err != nil {
		return execadapter.Result{}, err
	}
	runtimeState, preflightErr = m.recheckRuntimeStateAtExecution(runtimeState)
	if preflightErr != nil {
		return execadapter.Result{}, preflightErr
	}

	executionPath := "native-go"

	envOverrides := copyEnv(options.ExtraEnv)
	envOverrides["INSTALL_DIR"] = runtimeState.Paths.InstallDir
	var err error
	envOverrides, err = sanitizeEnvOverrides(
		"update",
		envOverrides,
		updateEnvOverrideAllowlist,
		envOverrideTrustPolicy{
			AllowTrustBoundaryEnv: options.AllowTrustBoundaryEnv,
			PrivilegedContext:     m.privilegedExecutionScope,
		},
	)
	if err != nil {
		return execadapter.Result{}, err
	}
	providerValue := string(runtimeState.Provider.Name)

	m.logger.Debug(
		"update adapter request assembled",
		"script_path", executionPath,
		"provider", providerValue,
		"install_dir", runtimeState.Paths.InstallDir,
		"options_contract", "install_dir_only",
		"allow_non_default_install_dir", options.AllowNonDefaultInstallDir,
		"working_dir", m.repoRoot,
		"runtime_env_file", runtimeState.Paths.EnvFile,
		"runtime_compose_file", runtimeState.Paths.ComposeFile,
		"env_override_keys", sortedKeys(envOverrides),
		"env_overrides", execadapter.RedactEnvSnapshot(envOverrides),
	)
	m.logger.Info(
		"update adapter start",
		"script_path", executionPath,
		"provider", providerValue,
		"install_dir", runtimeState.Paths.InstallDir,
	)

	runtimeState, preflightErr = m.recheckRuntimeStateAtExecution(runtimeState)
	if preflightErr != nil {
		return execadapter.Result{}, preflightErr
	}
	if err := enforceInstallDirDestructivePolicy("update", runtimeState.Paths.InstallDir, options.AllowNonDefaultInstallDir); err != nil {
		return execadapter.Result{}, err
	}
	envOverrides["INSTALL_DIR"] = runtimeState.Paths.InstallDir

	result, runErr := m.updateGoNative(ctx, runtimeState, envOverrides)

	summary := ParseUpdateLifecycle(result, runErr)
	m.logger.Debug(
		"update adapter parsed lifecycle summary",
		"script_path", executionPath,
		"status", summary.Status,
		"provider", normalizeLogValue(summary.Provider, providerValue),
		"install_dir", normalizeLogValue(summary.InstallDir, runtimeState.Paths.InstallDir),
		"source_ref", normalizeLogValue(summary.SourceRef, "n/a"),
		"active_image", normalizeLogValue(summary.ActiveImage, "n/a"),
		"rollback_triggered", summary.RollbackTriggered,
		"parse_diagnostics", summary.ParseDiagnostics,
	)

	failedSummaryErr := error(nil)
	if summary.Status == UpdateStatusFailed {
		failedSummaryErr = fmt.Errorf("update lifecycle parse failed: %s", strings.Join(summary.ParseDiagnostics, "; "))
	}

	if runErr != nil || summary.RollbackTriggered || failedSummaryErr != nil {
		reportedErr := runErr
		if reportedErr == nil && failedSummaryErr != nil {
			reportedErr = failedSummaryErr
		}
		if reportedErr == nil {
			reportedErr = fmt.Errorf("update lifecycle triggered rollback")
		}
		m.logger.Error(
			"update adapter failed",
			"script_path", executionPath,
			"status", summary.Status,
			"provider", normalizeLogValue(summary.Provider, providerValue),
			"install_dir", normalizeLogValue(summary.InstallDir, runtimeState.Paths.InstallDir),
			"source_ref", normalizeLogValue(summary.SourceRef, "n/a"),
			"active_image", normalizeLogValue(summary.ActiveImage, "n/a"),
			"rollback_triggered", summary.RollbackTriggered,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", execadapter.RedactText(reportedErr.Error()),
		)
		return result, reportedErr
	}

	m.logger.Info(
		"update adapter finish",
		"script_path", executionPath,
		"status", summary.Status,
		"provider", normalizeLogValue(summary.Provider, providerValue),
		"install_dir", normalizeLogValue(summary.InstallDir, runtimeState.Paths.InstallDir),
		"source_ref", normalizeLogValue(summary.SourceRef, "n/a"),
		"active_image", normalizeLogValue(summary.ActiveImage, "n/a"),
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
		"stderr_summary", result.StderrSummary,
	)

	return result, nil
}

func ParseUpdateLifecycle(result execadapter.Result, runErr error) UpdateLifecycleSummary {
	summary := UpdateLifecycleSummary{
		Status:           UpdateStatusFailed,
		OperatorHints:    make([]string, 0, 4),
		ParseDiagnostics: make([]string, 0, 4),
	}

	lines := splitLifecycleLines(result.Stdout)
	stderrLines := splitLifecycleLines(result.Stderr)
	seenCompletionMarker := false
	seenAlreadyUpToDate := false
	rollbackSuccessConfirmed := false
	rollbackFailureMarker := false

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
		case strings.HasPrefix(line, "Configured source:"):
			summary.SourceRef = parseLifecycleField(line, "Configured source:")
		case strings.HasPrefix(line, "Source:"):
			summary.SourceRef = parseLifecycleField(line, "Source:")
		case strings.HasPrefix(line, "Active:"):
			summary.ActiveImage = parseLifecycleField(line, "Active:")
		case strings.HasPrefix(line, "Prepared rollback image:"):
			summary.PreparedRollbackImage = parseLifecycleField(line, "Prepared rollback image:")
		case strings.HasPrefix(line, "Rolling back "):
			summary.RollbackTriggered = true
		case strings.Contains(strings.ToLower(line), "previous image restored"):
			rollbackSuccessConfirmed = true
		case strings.Contains(strings.ToLower(line), "rollback failed"):
			rollbackFailureMarker = true
		case strings.HasPrefix(line, "Image is already up to date:"):
			seenAlreadyUpToDate = true
			summary.ActiveImage = parseLifecycleField(line, "Image is already up to date:")
		case strings.HasSuffix(line, " update complete"):
			seenCompletionMarker = true
		case strings.HasPrefix(line, "Health:"):
			appendUniqueText(&summary.OperatorHints, line)
		case strings.HasPrefix(line, "Logs:"):
			appendUniqueText(&summary.OperatorHints, line)
		}
	}
	for _, rawLine := range stderrLines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), "previous image restored") {
			rollbackSuccessConfirmed = true
		}
		if strings.Contains(strings.ToLower(line), "rollback failed") {
			rollbackFailureMarker = true
		}
	}

	switch {
	case runErr != nil && summary.RollbackTriggered && rollbackSuccessConfirmed && !rollbackFailureMarker:
		summary.Status = UpdateStatusRolledBack
	case runErr != nil:
		summary.Status = UpdateStatusFailed
	case summary.RollbackTriggered:
		summary.Status = UpdateStatusFailed
		appendUniqueText(&summary.ParseDiagnostics, "rollback marker detected without command error")
	case seenAlreadyUpToDate:
		summary.Status = UpdateStatusAlreadyUpToDate
	case seenCompletionMarker:
		summary.Status = UpdateStatusUpdated
	default:
		summary.Status = UpdateStatusFailed
		appendUniqueText(&summary.ParseDiagnostics, "update output did not contain explicit completion marker; outcome is unknown")
	}

	if strings.TrimSpace(summary.Provider) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "provider marker is missing in update output")
	}
	if strings.TrimSpace(summary.InstallDir) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "install dir marker is missing in update output")
	}
	if strings.TrimSpace(summary.SourceRef) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "source marker is missing in update output")
	}
	if strings.TrimSpace(summary.ActiveImage) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "active image marker is missing in update output")
	}
	if summary.RollbackTriggered && !rollbackSuccessConfirmed {
		appendUniqueText(&summary.ParseDiagnostics, "rollback marker detected without rollback success confirmation")
	}
	if summary.RollbackTriggered && rollbackFailureMarker {
		appendUniqueText(&summary.ParseDiagnostics, "rollback failure marker detected in update output")
	}

	return summary
}
