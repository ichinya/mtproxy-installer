package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mtproxy-installer/app/internal/docker"
	"mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
)

type restartCommandOptions struct {
	service         string
	timeoutSeconds  int
	timeoutProvided bool
}

func runRestart(ctx commandContext) error {
	ctx.Logger.Info("restart command entry", "args_count", len(ctx.Args))

	options, err := parseRestartCommandArgs(ctx.Args)
	if err != nil {
		ctx.Logger.Error("restart command argument parse failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	runtimeState, err := runtimeLoad(runtime.LoadOptions{
		Logger: ctx.Logger,
	})
	if err != nil {
		ctx.Logger.Error("restart runtime load failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	selectedService, usedDefault, err := resolveProviderDefaultService(runtimeState.Provider.Name, options.service)
	if err != nil {
		ctx.Logger.Error("restart service resolution failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Info(
		"restart detected provider",
		"provider", runtimeState.Provider.Name,
		"install_dir", runtimeState.Paths.InstallDir,
		"service", selectedService,
	)
	ctx.Logger.Debug(
		"restart parsed options",
		"service", selectedService,
		"default_service_used", usedDefault,
		"timeout_provided", options.timeoutProvided,
		"timeout_seconds", options.timeoutSeconds,
	)

	composeRunner, err := newCompose(runtimeState, ctx.Logger)
	if err != nil {
		ctx.Logger.Error("restart compose adapter init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	orchestrator, err := docker.NewRestartOrchestrator(docker.RestartOrchestratorOptions{
		Compose: composeRunner,
		Logger:  ctx.Logger,
	})
	if err != nil {
		ctx.Logger.Error("restart orchestrator init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Info(
		"restart command begin",
		"provider", runtimeState.Provider.Name,
		"service", selectedService,
		"timeout_provided", options.timeoutProvided,
		"timeout_seconds", options.timeoutSeconds,
	)

	restartResult, err := orchestrator.Run(context.Background(), docker.RestartOptions{
		Service:         selectedService,
		TimeoutSeconds:  options.timeoutSeconds,
		TimeoutProvided: options.timeoutProvided,
	})
	if err != nil {
		ctx.Logger.Error(
			"restart command failed",
			"provider", runtimeState.Provider.Name,
			"service", selectedService,
			"timeout_provided", options.timeoutProvided,
			"timeout_seconds", options.timeoutSeconds,
			"pre_status", restartResult.PreState.Status,
			"pre_reason", restartResult.PreState.Reason,
			"restart_elapsed", restartResult.Restart.Elapsed,
			"restart_exit_status", restartResult.Restart.ExitCode,
			"stderr_summary", restartResult.Restart.StderrSummary,
			"error", redactForCommand(ctx.Command, err.Error()),
		)
		return err
	}

	var telemtSummary *telemt.StatusSummary
	var telemtSummaryErr error
	if runtimeState.Provider.Name == runtime.ProviderTelemt {
		collected, collectErr := collectTelemtRuntimeStatus(ctx, runtimeState)
		if collectErr != nil {
			telemtSummaryErr = collectErr
			ctx.Logger.Warn(
				"restart telemt post-check failed",
				"provider", runtimeState.Provider.Name,
				"service", selectedService,
				"error", redactForCommand(ctx.Command, collectErr.Error()),
			)
		} else {
			telemtSummary = &collected
			if collected.RuntimeStatus == telemt.RuntimeStatusHealthy {
				ctx.Logger.Info(
					"restart telemt post-check",
					"runtime_status", collected.RuntimeStatus,
					"compose_state", collected.ComposeStatus,
					"health_state", collected.HealthStatus,
					"link_state", collected.LinkStatus,
				)
			} else {
				ctx.Logger.Warn(
					"restart telemt post-check",
					"runtime_status", collected.RuntimeStatus,
					"compose_state", collected.ComposeStatus,
					"health_state", collected.HealthStatus,
					"link_state", collected.LinkStatus,
					"degraded_reasons", collected.DegradedReason,
				)
			}
		}
	}

	degraded, degradedReasons := evaluateRestartDegradation(restartResult, telemtSummary, telemtSummaryErr)
	summary := renderRestartSummary(runtimeState.Provider.Name, restartResult, telemtSummary, telemtSummaryErr, degradedReasons)
	if err := writeCommandOutput(ctx.Stdout, summary); err != nil {
		return err
	}

	if degraded {
		ctx.Logger.Warn(
			"restart command completed with degraded post-check",
			"provider", runtimeState.Provider.Name,
			"service", selectedService,
			"timeout_provided", options.timeoutProvided,
			"timeout_seconds", options.timeoutSeconds,
			"restart_elapsed", restartResult.Restart.Elapsed,
			"restart_exit_status", restartResult.Restart.ExitCode,
			"pre_status", restartResult.PreState.Status,
			"pre_reason", restartResult.PreState.Reason,
			"post_status", restartResult.PostState.Status,
			"post_reason", restartResult.PostState.Reason,
			"degraded_reasons", degradedReasons,
		)
	} else {
		ctx.Logger.Info(
			"restart command end",
			"provider", runtimeState.Provider.Name,
			"service", selectedService,
			"timeout_provided", options.timeoutProvided,
			"timeout_seconds", options.timeoutSeconds,
			"restart_elapsed", restartResult.Restart.Elapsed,
			"restart_exit_status", restartResult.Restart.ExitCode,
			"pre_status", restartResult.PreState.Status,
			"pre_reason", restartResult.PreState.Reason,
			"post_status", restartResult.PostState.Status,
			"post_reason", restartResult.PostState.Reason,
		)
	}

	return nil
}

func parseRestartCommandArgs(args []string) (restartCommandOptions, error) {
	options := restartCommandOptions{}

	for idx := 0; idx < len(args); idx++ {
		token := strings.TrimSpace(args[idx])
		if token == "" {
			return restartCommandOptions{}, fmt.Errorf("restart command argument #%d is empty", idx+1)
		}

		switch {
		case token == "--timeout":
			if idx+1 >= len(args) {
				return restartCommandOptions{}, errorsForMissingValue("restart", "--timeout")
			}
			idx++
			timeout, err := parseRestartTimeout(args[idx])
			if err != nil {
				return restartCommandOptions{}, err
			}
			options.timeoutSeconds = timeout
			options.timeoutProvided = true
		case strings.HasPrefix(token, "--timeout="):
			value := strings.TrimSpace(strings.TrimPrefix(token, "--timeout="))
			timeout, err := parseRestartTimeout(value)
			if err != nil {
				return restartCommandOptions{}, err
			}
			options.timeoutSeconds = timeout
			options.timeoutProvided = true
		case strings.HasPrefix(token, "-"):
			return restartCommandOptions{}, fmt.Errorf("restart command flag %q is not supported", token)
		default:
			if options.service != "" {
				return restartCommandOptions{}, fmt.Errorf(
					"restart command accepts at most one service argument, got %q and %q",
					options.service,
					token,
				)
			}
			options.service = token
		}
	}

	return options, nil
}

func parseRestartTimeout(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errorsForMissingValue("restart", "--timeout")
	}
	timeout, err := strconv.Atoi(trimmed)
	if err != nil || timeout < 0 {
		return 0, fmt.Errorf("restart command --timeout value %q is invalid", value)
	}
	return timeout, nil
}

func evaluateRestartDegradation(
	restartResult docker.RestartResult,
	telemtSummary *telemt.StatusSummary,
	telemtSummaryErr error,
) (bool, []string) {
	reasons := make([]string, 0, 4)
	appendReason := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		for _, existing := range reasons {
			if existing == trimmed {
				return
			}
		}
		reasons = append(reasons, trimmed)
	}

	if restartResult.PostCheckDegraded() {
		appendReason(fmt.Sprintf(
			"compose post-check is %s (%s)",
			restartResult.PostState.Status,
			normalizeReasonOrNA(restartResult.PostState.Reason),
		))
	}
	if telemtSummaryErr != nil {
		appendReason("telemt post-check failed")
	}
	if telemtSummary != nil && telemtSummary.RuntimeStatus != telemt.RuntimeStatusHealthy {
		appendReason(fmt.Sprintf("telemt runtime status is %s", telemtSummary.RuntimeStatus))
	}

	return len(reasons) > 0, reasons
}

func renderRestartSummary(
	provider runtime.Provider,
	restartResult docker.RestartResult,
	telemtSummary *telemt.StatusSummary,
	telemtSummaryErr error,
	degradedReasons []string,
) string {
	lines := []string{
		fmt.Sprintf("Provider: %s", provider),
		fmt.Sprintf("Service: %s", restartResult.Service),
		fmt.Sprintf(
			"Restart command: exit=%d elapsed=%s",
			restartResult.Restart.ExitCode,
			restartResult.Restart.Elapsed,
		),
		fmt.Sprintf(
			"Compose pre-check: %s (%s)",
			restartResult.PreState.Status,
			normalizeReasonOrNA(restartResult.PreState.Reason),
		),
		fmt.Sprintf(
			"Compose post-check: %s (%s)",
			restartResult.PostState.Status,
			normalizeReasonOrNA(restartResult.PostState.Reason),
		),
	}

	if telemtSummary != nil {
		lines = append(lines, fmt.Sprintf(
			"Telemt post-check: %s (compose=%s, health=%s, link=%s)",
			telemtSummary.RuntimeStatus,
			telemtSummary.ComposeStatus,
			telemtSummary.HealthStatus,
			telemtSummary.LinkStatus,
		))
	}
	if telemtSummaryErr != nil {
		lines = append(lines, fmt.Sprintf(
			"Telemt post-check: error (%s)",
			redactForCommand("restart", telemtSummaryErr.Error()),
		))
	}

	if len(degradedReasons) == 0 {
		lines = append(lines, "Restart result: healthy")
	} else {
		lines = append(lines, fmt.Sprintf("Warning: restart completed with degradation: %s", strings.Join(degradedReasons, ", ")))
	}

	return strings.Join(lines, "\n")
}

func normalizeReasonOrNA(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "n/a"
	}
	return trimmed
}
