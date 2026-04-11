package scripts

import (
	"context"

	execadapter "mtproxy-installer/app/internal/exec"
)

type UpdateOptions struct {
	// Runtime/image settings are sourced from the existing runtime .env contract in update.sh.
	InstallDir                string
	AllowNonDefaultInstallDir bool
	ExtraEnv                  map[string]string
}

func (m *Manager) Update(ctx context.Context, options UpdateOptions) (execadapter.Result, error) {
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

	scriptPath, err := m.resolveScriptPath(updateScriptName)
	if err != nil {
		return execadapter.Result{}, err
	}

	envOverrides := copyEnv(options.ExtraEnv)
	envOverrides["INSTALL_DIR"] = runtimeState.Paths.InstallDir
	envOverrides, err = sanitizeEnvOverrides("update", envOverrides, updateEnvOverrideAllowlist)
	if err != nil {
		return execadapter.Result{}, err
	}
	providerValue := string(runtimeState.Provider.Name)

	m.logger.Debug(
		"update adapter request assembled",
		"script_path", scriptPath,
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
		"script_path", scriptPath,
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
			"update adapter failed",
			"script_path", scriptPath,
			"provider", providerValue,
			"install_dir", runtimeState.Paths.InstallDir,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", execadapter.RedactText(runErr.Error()),
		)
		return result, runErr
	}

	m.logger.Info(
		"update adapter finish",
		"script_path", scriptPath,
		"provider", providerValue,
		"install_dir", runtimeState.Paths.InstallDir,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
		"stderr_summary", result.StderrSummary,
	)

	return result, nil
}
