package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/output"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/scripts"
)

type updateCommandOptions struct {
	installDir                string
	allowNonDefaultInstallDir bool
	extraEnv                  map[string]string
	allowTrustBoundaryEnv     bool
}

var updateUnsupportedFlags = map[string]struct{}{
	"--provider":            {},
	"--port":                {},
	"--api-port":            {},
	"--tls-domain":          {},
	"--public-ip":           {},
	"--secret":              {},
	"--proxy-user":          {},
	"--telemt-image":        {},
	"--telemt-image-source": {},
	"--mtg-image":           {},
	"--mtg-image-source":    {},
}

func runUpdate(ctx commandContext) error {
	ctx.Logger.Info("update command entry", "args_count", len(ctx.Args))

	options, err := parseUpdateCommandArgs(ctx.Args)
	if err != nil {
		ctx.Logger.Error("update command argument parse failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	envPreview := buildUpdateEnvPreview(options)
	ctx.Logger.Debug(
		"update command parsed options",
		"install_dir", options.installDir,
		"allow_non_default_install_dir", options.allowNonDefaultInstallDir,
		"mapped_env_override_keys", sortedMapKeys(envPreview),
		"mapped_env_overrides", execadapter.RedactEnvSnapshot(envPreview),
		"allow_trust_boundary_env", options.allowTrustBoundaryEnv,
	)

	manager, err := newLifecycleScriptsManager(ctx.Logger)
	if err != nil {
		ctx.Logger.Error("update scripts manager init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Info(
		"update lifecycle begin",
		"install_dir", options.installDir,
		"allow_non_default_install_dir", options.allowNonDefaultInstallDir,
		"allow_trust_boundary_env", options.allowTrustBoundaryEnv,
	)

	result, runErr := manager.Update(context.Background(), scripts.UpdateOptions{
		InstallDir:                options.installDir,
		AllowNonDefaultInstallDir: options.allowNonDefaultInstallDir,
		ExtraEnv:                  options.extraEnv,
		AllowTrustBoundaryEnv:     options.allowTrustBoundaryEnv,
	})
	if runErr != nil {
		summary := scripts.ParseUpdateLifecycle(result, runErr)
		ctx.Logger.Error(
			"update lifecycle failed",
			"status", summary.Status,
			"provider", normalizeLogOptional(summary.Provider),
			"install_dir", normalizeLogOptional(summary.InstallDir),
			"source_ref", normalizeLogOptional(summary.SourceRef),
			"active_image", normalizeLogOptional(summary.ActiveImage),
			"rollback_triggered", summary.RollbackTriggered,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", redactForCommand(ctx.Command, errorTextOrNone(runErr)),
			"parse_diagnostics", summary.ParseDiagnostics,
		)
		return runErr
	}

	summary := scripts.ParseUpdateLifecycle(result, nil)
	ctx.Logger.Debug(
		"update lifecycle parsed summary",
		"status", summary.Status,
		"provider", normalizeLogOptional(summary.Provider),
		"install_dir", normalizeLogOptional(summary.InstallDir),
		"source_ref", normalizeLogOptional(summary.SourceRef),
		"active_image", normalizeLogOptional(summary.ActiveImage),
		"rollback_triggered", summary.RollbackTriggered,
		"parse_diagnostics", summary.ParseDiagnostics,
	)

	if summary.Status == scripts.UpdateStatusFailed {
		parseError := fmt.Errorf(
			"update lifecycle parse failed: %s",
			strings.Join(summary.ParseDiagnostics, "; "),
		)
		ctx.Logger.Error(
			"update lifecycle failed",
			"status", summary.Status,
			"provider", normalizeLogOptional(summary.Provider),
			"install_dir", normalizeLogOptional(summary.InstallDir),
			"source_ref", normalizeLogOptional(summary.SourceRef),
			"active_image", normalizeLogOptional(summary.ActiveImage),
			"rollback_triggered", summary.RollbackTriggered,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", redactForCommand(ctx.Command, parseError.Error()),
			"parse_diagnostics", summary.ParseDiagnostics,
		)
		return parseError
	}

	if strings.TrimSpace(summary.Provider) == "" || strings.TrimSpace(summary.InstallDir) == "" {
		parseError := fmt.Errorf("update lifecycle parse failed: required markers are missing in update output")
		ctx.Logger.Error(
			"update lifecycle failed",
			"status", summary.Status,
			"provider", normalizeLogOptional(summary.Provider),
			"install_dir", normalizeLogOptional(summary.InstallDir),
			"source_ref", normalizeLogOptional(summary.SourceRef),
			"active_image", normalizeLogOptional(summary.ActiveImage),
			"rollback_triggered", summary.RollbackTriggered,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", redactForCommand(ctx.Command, parseError.Error()),
			"parse_diagnostics", summary.ParseDiagnostics,
		)
		return parseError
	}

	if err := writeCommandOutput(ctx.Stdout, output.RenderUpdateLifecycle(summary)); err != nil {
		return err
	}

	ctx.Logger.Info(
		"update lifecycle finish",
		"status", summary.Status,
		"provider", summary.Provider,
		"install_dir", summary.InstallDir,
		"source_ref", normalizeLogOptional(summary.SourceRef),
		"active_image", normalizeLogOptional(summary.ActiveImage),
		"rollback_triggered", summary.RollbackTriggered,
	)

	return nil
}

func parseUpdateCommandArgs(args []string) (updateCommandOptions, error) {
	if err := rejectUnsupportedUpdateFlags(args); err != nil {
		return updateCommandOptions{}, err
	}

	envFlag := newEnvOverrideFlag("update")
	options := updateCommandOptions{
		installDir: runtime.DefaultInstallDir,
	}

	flagSet := flag.NewFlagSet("update", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.StringVar(&options.installDir, "install-dir", options.installDir, "runtime install dir")
	flagSet.BoolVar(&options.allowNonDefaultInstallDir, "allow-non-default-install-dir", false, "allow non-default install dir")
	flagSet.BoolVar(&options.allowTrustBoundaryEnv, "allow-trust-boundary-env", false, "allow trust-boundary --env overrides")
	flagSet.Var(envFlag, "env", "extra env override in KEY=VALUE form (repeatable)")

	if err := flagSet.Parse(args); err != nil {
		return updateCommandOptions{}, fmt.Errorf("update command flag parse failed: %w", err)
	}

	if flagSet.NArg() > 0 {
		return updateCommandOptions{}, fmt.Errorf("update command does not accept positional arguments: %s", strings.Join(flagSet.Args(), " "))
	}

	options.installDir = strings.TrimSpace(options.installDir)
	if options.installDir == "" {
		options.installDir = runtime.DefaultInstallDir
	}
	options.extraEnv = envFlag.Values()
	if err := validateTrustBoundaryEnvOverrides("update", options.extraEnv, options.allowTrustBoundaryEnv); err != nil {
		return updateCommandOptions{}, err
	}

	return options, nil
}

func rejectUnsupportedUpdateFlags(args []string) error {
	for _, raw := range args {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		if token == "--" {
			break
		}
		if !strings.HasPrefix(token, "--") {
			continue
		}
		flagName := token
		if idx := strings.Index(flagName, "="); idx >= 0 {
			flagName = flagName[:idx]
		}
		if _, blocked := updateUnsupportedFlags[flagName]; blocked {
			return fmt.Errorf(
				"update command flag %s is unsupported; update always uses provider/image from installed runtime",
				flagName,
			)
		}
	}
	return nil
}

func buildUpdateEnvPreview(options updateCommandOptions) map[string]string {
	preview := copyStringMap(options.extraEnv)
	preview["INSTALL_DIR"] = options.installDir
	return preview
}

func errorTextOrNone(err error) string {
	if err == nil {
		return "none"
	}
	return err.Error()
}
