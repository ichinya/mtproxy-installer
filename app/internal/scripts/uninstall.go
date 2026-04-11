package scripts

import (
	"context"
	"fmt"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

type UninstallOptions struct {
	InstallDir                string
	KeepData                  bool
	DetectedProvider          runtime.Provider
	AllowNonDefaultInstallDir bool
	ExtraEnv                  map[string]string
}

func (m *Manager) Uninstall(ctx context.Context, options UninstallOptions) (execadapter.Result, error) {
	runtimeState, preflightErr := m.preflightRuntimeInstallDir(options.InstallDir)
	if preflightErr != nil {
		return execadapter.Result{}, preflightErr
	}
	if err := enforceInstallDirDestructivePolicy("uninstall", runtimeState.Paths.InstallDir, options.AllowNonDefaultInstallDir); err != nil {
		return execadapter.Result{}, err
	}
	runtimeState, preflightErr = m.recheckRuntimeStateAtExecution(runtimeState)
	if preflightErr != nil {
		return execadapter.Result{}, preflightErr
	}

	scriptPath, err := m.resolveScriptPath(uninstallScriptName)
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
	envOverrides, err = sanitizeEnvOverrides("uninstall", envOverrides, uninstallEnvOverrideAllowlist)
	if err != nil {
		return execadapter.Result{}, err
	}

	providerHint := strings.TrimSpace(string(options.DetectedProvider))
	if providerHint == "" {
		providerHint = string(runtimeState.Provider.Name)
	} else {
		normalizedHint, normalizeErr := normalizeProvider(runtime.Provider(providerHint))
		if normalizeErr != nil {
			return execadapter.Result{}, normalizeErr
		}
		if !strings.EqualFold(normalizedHint, string(runtimeState.Provider.Name)) {
			return execadapter.Result{}, fmt.Errorf(
				"INSTALL_DIR preflight failed for %q: detected provider %q does not match runtime provider %q",
				runtimeState.Paths.InstallDir,
				providerHint,
				runtimeState.Provider.Name,
			)
		}
		providerHint = normalizedHint
	}

	if runtimeState.Provider.Name == runtime.ProviderMTG {
		unsupportedErr := fmt.Errorf(
			"uninstall adapter does not support provider %q: current uninstall.sh flow is telemt-biased",
			runtimeState.Provider.Name,
		)
		m.logger.Error(
			"uninstall adapter rejected unsupported provider",
			"provider_hint", providerHint,
			"runtime_provider", runtimeState.Provider.Name,
			"install_dir", runtimeState.Paths.InstallDir,
			"error", execadapter.RedactText(unsupportedErr.Error()),
		)
		return execadapter.Result{}, unsupportedErr
	}

	m.logger.Warn(
		"uninstall adapter destructive execution requested",
		"script_path", scriptPath,
		"provider_hint", providerHint,
		"install_dir", runtimeState.Paths.InstallDir,
		"keep_data", options.KeepData,
		"allow_non_default_install_dir", options.AllowNonDefaultInstallDir,
		"constraint", "current uninstall.sh path remains telemt-biased",
	)
	m.logger.Debug(
		"uninstall adapter request assembled",
		"script_path", scriptPath,
		"working_dir", m.repoRoot,
		"env_override_keys", sortedKeys(envOverrides),
		"env_overrides", execadapter.RedactEnvSnapshot(envOverrides),
	)
	m.logger.Info(
		"uninstall adapter start",
		"script_path", scriptPath,
		"provider_hint", providerHint,
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

	result, runErr := m.runner.Run(ctx, execadapter.Request{
		Command:          m.bashPath,
		Args:             []string{scriptPath},
		WorkingDir:       m.repoRoot,
		EnvOverrides:     envOverrides,
		InheritParentEnv: false,
		AllowedEnvKeys:   sortedKeys(envOverrides),
		UseSafePath:      true,
	})
	if runErr != nil {
		m.logger.Error(
			"uninstall adapter failed",
			"script_path", scriptPath,
			"provider_hint", providerHint,
			"install_dir", runtimeState.Paths.InstallDir,
			"keep_data", options.KeepData,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", execadapter.RedactText(runErr.Error()),
		)
		return result, runErr
	}

	m.logger.Info(
		"uninstall adapter finish",
		"script_path", scriptPath,
		"provider_hint", providerHint,
		"install_dir", runtimeState.Paths.InstallDir,
		"keep_data", options.KeepData,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
		"stderr_summary", result.StderrSummary,
	)

	return result, nil
}
