package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mtproxy-installer/app/internal/docker"
	"mtproxy-installer/app/internal/runtime"
)

type logsCommandOptions struct {
	service    string
	tail       string
	follow     bool
	timestamps bool
	noColor    bool
}

func runLogs(ctx commandContext) error {
	ctx.Logger.Info("logs command entry", "args_count", len(ctx.Args))

	options, err := parseLogsCommandArgs(ctx.Args)
	if err != nil {
		ctx.Logger.Error("logs command argument parse failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	runtimeState, err := runtimeLoad(runtime.LoadOptions{
		Logger: ctx.Logger,
	})
	if err != nil {
		ctx.Logger.Error("logs runtime load failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	selectedService, usedDefault, err := resolveProviderDefaultService(runtimeState.Provider.Name, options.service)
	if err != nil {
		ctx.Logger.Error("logs service resolution failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Info(
		"logs detected provider",
		"provider", runtimeState.Provider.Name,
		"install_dir", runtimeState.Paths.InstallDir,
		"service", selectedService,
	)
	ctx.Logger.Debug(
		"logs parsed options",
		"service", selectedService,
		"default_service_used", usedDefault,
		"tail", options.tail,
		"follow", options.follow,
		"timestamps", options.timestamps,
		"no_color", options.noColor,
	)

	composeRunner, err := newCompose(runtimeState, ctx.Logger)
	if err != nil {
		ctx.Logger.Error("logs compose adapter init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	logsRunner, err := docker.NewLogsRunner(docker.LogsRunnerOptions{
		Compose: composeRunner,
		Logger:  ctx.Logger,
	})
	if err != nil {
		ctx.Logger.Error("logs adapter init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Info(
		"logs command start",
		"provider", runtimeState.Provider.Name,
		"service", selectedService,
		"tail", options.tail,
		"follow", options.follow,
		"timestamps", options.timestamps,
		"no_color", options.noColor,
	)

	result, err := logsRunner.Run(context.Background(), docker.LogsOptions{
		Service:    selectedService,
		Tail:       options.tail,
		Follow:     options.follow,
		Timestamps: options.timestamps,
		NoColor:    options.noColor,
		Stdout:     ctx.Stdout,
		Stderr:     ctx.Stderr,
	})
	if err != nil {
		ctx.Logger.Error(
			"logs command failed",
			"provider", runtimeState.Provider.Name,
			"service", selectedService,
			"tail", options.tail,
			"follow", options.follow,
			"timestamps", options.timestamps,
			"no_color", options.noColor,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"error", redactForCommand(ctx.Command, err.Error()),
		)
		return err
	}

	ctx.Logger.Info(
		"logs command finish",
		"provider", runtimeState.Provider.Name,
		"service", selectedService,
		"tail", options.tail,
		"follow", options.follow,
		"timestamps", options.timestamps,
		"no_color", options.noColor,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
	)
	return nil
}

func parseLogsCommandArgs(args []string) (logsCommandOptions, error) {
	options := logsCommandOptions{
		tail: "all",
	}

	for idx := 0; idx < len(args); idx++ {
		token := strings.TrimSpace(args[idx])
		if token == "" {
			return logsCommandOptions{}, fmt.Errorf("logs command argument #%d is empty", idx+1)
		}

		switch {
		case token == "-f" || token == "--follow":
			options.follow = true
		case token == "-t" || token == "--timestamps":
			options.timestamps = true
		case token == "--no-color":
			options.noColor = true
		case token == "--tail":
			if idx+1 >= len(args) {
				return logsCommandOptions{}, errorsForMissingValue("logs", "--tail")
			}
			idx++
			value := strings.TrimSpace(args[idx])
			if err := validateLogsTailValue(value); err != nil {
				return logsCommandOptions{}, err
			}
			options.tail = value
		case strings.HasPrefix(token, "--tail="):
			value := strings.TrimSpace(strings.TrimPrefix(token, "--tail="))
			if err := validateLogsTailValue(value); err != nil {
				return logsCommandOptions{}, err
			}
			options.tail = value
		case strings.HasPrefix(token, "-"):
			return logsCommandOptions{}, fmt.Errorf("logs command flag %q is not supported", token)
		default:
			if options.service != "" {
				return logsCommandOptions{}, fmt.Errorf(
					"logs command accepts at most one service argument, got %q and %q",
					options.service,
					token,
				)
			}
			options.service = token
		}
	}

	return options, nil
}

func validateLogsTailValue(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errorsForMissingValue("logs", "--tail")
	}
	if strings.EqualFold(trimmed, "all") {
		return nil
	}
	numeric, err := strconv.Atoi(trimmed)
	if err != nil || numeric < 0 {
		return fmt.Errorf("logs command --tail value %q is invalid", value)
	}
	return nil
}

func errorsForMissingValue(command string, flag string) error {
	return fmt.Errorf("%s command requires a value for %s", command, flag)
}

func resolveProviderDefaultService(provider runtime.Provider, explicitService string) (string, bool, error) {
	service := strings.TrimSpace(explicitService)
	if service != "" {
		return service, false, nil
	}

	switch provider {
	case runtime.ProviderTelemt, runtime.ProviderMTG:
		return string(provider), true, nil
	default:
		return "", false, fmt.Errorf("provider %q is unsupported for service auto-selection", provider)
	}
}
