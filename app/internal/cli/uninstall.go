package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"mtproxy-installer/app/internal/output"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/scripts"
)

type uninstallCommandOptions struct {
	installDir                string
	allowNonDefaultInstallDir bool
	keepData                  bool
	yes                       bool
}

var uninstallUnsupportedFlags = map[string]struct{}{
	"--provider":                 {},
	"--port":                     {},
	"--api-port":                 {},
	"--tls-domain":               {},
	"--public-ip":                {},
	"--secret":                   {},
	"--proxy-user":               {},
	"--telemt-image":             {},
	"--telemt-image-source":      {},
	"--mtg-image":                {},
	"--mtg-image-source":         {},
	"--env":                      {},
	"--allow-trust-boundary-env": {},
}

func runUninstall(ctx commandContext) error {
	ctx.Logger.Info("uninstall command entry", "args_count", len(ctx.Args))

	options, err := parseUninstallCommandArgs(ctx.Args)
	if err != nil {
		ctx.Logger.Error("uninstall command argument parse failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Debug(
		"uninstall command parsed options",
		"strategy", scripts.UninstallStrategyTelemtOnly,
		"install_dir", options.installDir,
		"allow_non_default_install_dir", options.allowNonDefaultInstallDir,
		"keep_data", options.keepData,
		"yes", options.yes,
	)

	if !options.yes {
		err = fmt.Errorf("uninstall command requires --yes confirmation before destructive execution")
		ctx.Logger.Warn(
			"uninstall confirmation gate rejected",
			"strategy", scripts.UninstallStrategyTelemtOnly,
			"install_dir", options.installDir,
			"keep_data", options.keepData,
			"preflight_confirmation", "missing",
		)
		return err
	}

	runtimeState, err := runtimeLoad(runtime.LoadOptions{
		InstallDir: options.installDir,
		Logger:     ctx.Logger,
	})
	if err != nil {
		ctx.Logger.Error(
			"uninstall runtime preflight failed",
			"strategy", scripts.UninstallStrategyTelemtOnly,
			"install_dir", options.installDir,
			"keep_data", options.keepData,
			"preflight_confirmation", "confirmed",
			"error", redactForCommand(ctx.Command, err.Error()),
		)
		return fmt.Errorf("uninstall runtime preflight failed: %w", err)
	}

	detectedProvider, contractErr := scripts.ResolveUninstallProviderContract(runtimeState.Provider.Name, runtimeState.Provider.Name)
	if contractErr != nil {
		ctx.Logger.Error(
			"uninstall provider contract rejected runtime",
			"strategy", scripts.UninstallStrategyTelemtOnly,
			"detected_provider", runtimeState.Provider.Name,
			"install_dir", runtimeState.Paths.InstallDir,
			"keep_data", options.keepData,
			"preflight_confirmation", "confirmed",
			"error", redactForCommand(ctx.Command, contractErr.Error()),
		)
		return contractErr
	}

	ctx.Logger.Info(
		"uninstall preflight accepted",
		"strategy", scripts.UninstallStrategyTelemtOnly,
		"detected_provider", detectedProvider,
		"install_dir", runtimeState.Paths.InstallDir,
		"keep_data", options.keepData,
		"preflight_confirmation", "confirmed",
	)

	manager, err := newLifecycleScriptsManager(ctx.Logger)
	if err != nil {
		ctx.Logger.Error("uninstall scripts manager init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Warn(
		"uninstall lifecycle begin",
		"strategy", scripts.UninstallStrategyTelemtOnly,
		"detected_provider", detectedProvider,
		"install_dir", runtimeState.Paths.InstallDir,
		"allow_non_default_install_dir", options.allowNonDefaultInstallDir,
		"keep_data", options.keepData,
	)

	result, runErr := manager.Uninstall(context.Background(), scripts.UninstallOptions{
		InstallDir:                runtimeState.Paths.InstallDir,
		KeepData:                  options.keepData,
		DetectedProvider:          detectedProvider,
		AllowNonDefaultInstallDir: options.allowNonDefaultInstallDir,
	})
	summary := scripts.ParseUninstallLifecycle(result, runErr)
	ctx.Logger.Debug(
		"uninstall lifecycle parsed summary",
		"strategy", summary.Strategy,
		"provider", summary.Provider,
		"install_dir", summary.InstallDir,
		"keep_data", summary.KeepData,
		"cleanup_status", summary.CleanupStatus,
		"data_removed", summary.DataRemoved,
		"image_cleanup", summary.ImageCleanup,
		"operator_hints", summary.OperatorHints,
		"parse_diagnostics", summary.ParseDiagnostics,
	)

	if runErr != nil {
		ctx.Logger.Error(
			"uninstall lifecycle failed",
			"strategy", summary.Strategy,
			"provider", summary.Provider,
			"install_dir", summary.InstallDir,
			"keep_data", summary.KeepData,
			"cleanup_status", summary.CleanupStatus,
			"data_removed", summary.DataRemoved,
			"image_cleanup", summary.ImageCleanup,
			"parse_diagnostics", summary.ParseDiagnostics,
			"error", redactForCommand(ctx.Command, runErr.Error()),
		)
		return runErr
	}

	if err := validateUninstallLifecycleSummary(summary); err != nil {
		ctx.Logger.Error(
			"uninstall lifecycle failed",
			"strategy", summary.Strategy,
			"provider", summary.Provider,
			"install_dir", summary.InstallDir,
			"keep_data", summary.KeepData,
			"cleanup_status", summary.CleanupStatus,
			"data_removed", summary.DataRemoved,
			"image_cleanup", summary.ImageCleanup,
			"parse_diagnostics", summary.ParseDiagnostics,
			"error", redactForCommand(ctx.Command, err.Error()),
		)
		return err
	}

	if err := writeCommandOutput(ctx.Stdout, output.RenderUninstallLifecycle(summary)); err != nil {
		return err
	}

	ctx.Logger.Info(
		"uninstall lifecycle finish",
		"strategy", summary.Strategy,
		"provider", summary.Provider,
		"install_dir", summary.InstallDir,
		"keep_data", summary.KeepData,
		"cleanup_status", summary.CleanupStatus,
		"data_removed", summary.DataRemoved,
		"image_cleanup", summary.ImageCleanup,
	)

	return nil
}

func parseUninstallCommandArgs(args []string) (uninstallCommandOptions, error) {
	if err := rejectUnsupportedUninstallFlags(args); err != nil {
		return uninstallCommandOptions{}, err
	}

	options := uninstallCommandOptions{
		installDir: runtime.DefaultInstallDir,
	}

	flagSet := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.StringVar(&options.installDir, "install-dir", options.installDir, "runtime install dir")
	flagSet.BoolVar(&options.allowNonDefaultInstallDir, "allow-non-default-install-dir", false, "allow non-default install dir")
	flagSet.BoolVar(&options.keepData, "keep-data", false, "keep runtime data directory")
	flagSet.BoolVar(&options.yes, "yes", false, "confirm destructive uninstall")
	flagSet.BoolVar(&options.yes, "y", false, "confirm destructive uninstall")

	if err := flagSet.Parse(args); err != nil {
		return uninstallCommandOptions{}, fmt.Errorf("uninstall command flag parse failed: %w", err)
	}

	if flagSet.NArg() > 0 {
		return uninstallCommandOptions{}, fmt.Errorf("uninstall command does not accept positional arguments: %s", strings.Join(flagSet.Args(), " "))
	}

	options.installDir = strings.TrimSpace(options.installDir)
	if options.installDir == "" {
		options.installDir = runtime.DefaultInstallDir
	}

	return options, nil
}

func rejectUnsupportedUninstallFlags(args []string) error {
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

		if _, blocked := uninstallUnsupportedFlags[flagName]; blocked {
			return fmt.Errorf(
				"uninstall command flag %s is unsupported; v1 uninstall strategy is telemt-only and does not accept provider/image/env overrides",
				flagName,
			)
		}
	}

	return nil
}

func validateUninstallLifecycleSummary(summary scripts.UninstallLifecycleSummary) error {
	if len(summary.ParseDiagnostics) > 0 {
		return fmt.Errorf("uninstall lifecycle parse diagnostics present: %s", strings.Join(summary.ParseDiagnostics, "; "))
	}

	switch summary.CleanupStatus {
	case scripts.UninstallCleanupStatusCompleted, scripts.UninstallCleanupStatusCompletedKeepData:
		return nil
	case scripts.UninstallCleanupStatusPartial:
		return errors.New("uninstall lifecycle reported partial cleanup")
	case scripts.UninstallCleanupStatusBlockedUnsupportedProvider,
		scripts.UninstallCleanupStatusBlockedProviderMismatch,
		scripts.UninstallCleanupStatusBlockedAmbiguousProvider,
		scripts.UninstallCleanupStatusFailedPreflight,
		scripts.UninstallCleanupStatusFailed,
		scripts.UninstallCleanupStatusUnknown:
		return fmt.Errorf("uninstall lifecycle failed with cleanup status %q", summary.CleanupStatus)
	default:
		return fmt.Errorf("uninstall lifecycle returned unsupported cleanup status %q", summary.CleanupStatus)
	}
}
