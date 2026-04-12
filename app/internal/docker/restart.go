package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
)

type ComposeServiceState string

const (
	ComposeServiceStateRunning    ComposeServiceState = "running"
	ComposeServiceStateNotRunning ComposeServiceState = "not_running"
	ComposeServiceStateUnknown    ComposeServiceState = "unknown"
)

type RestartOptions struct {
	Service         string
	TimeoutSeconds  int
	TimeoutProvided bool
}

type RestartStateSnapshot struct {
	Checked       bool
	Status        ComposeServiceState
	Reason        string
	StderrSummary string
	Error         string
}

type RestartResult struct {
	Service         string
	TimeoutSeconds  int
	TimeoutProvided bool
	PreState        RestartStateSnapshot
	PostState       RestartStateSnapshot
	Restart         execadapter.Result
}

func (r RestartResult) PostCheckDegraded() bool {
	if !r.PostState.Checked {
		return true
	}
	return r.PostState.Status != ComposeServiceStateRunning
}

type RestartOrchestratorOptions struct {
	Compose composeRestartRunner
	Logger  *slog.Logger
}

type RestartOrchestrator struct {
	compose composeRestartRunner
	logger  *slog.Logger
}

type composeRestartRunner interface {
	Run(context.Context, ComposeCommand) (execadapter.Result, error)
}

func NewRestartOrchestrator(options RestartOrchestratorOptions) (*RestartOrchestrator, error) {
	if options.Compose == nil {
		return nil, errors.New("compose runner is required for restart command")
	}

	return &RestartOrchestrator{
		compose: options.Compose,
		logger:  fallbackLogger(options.Logger),
	}, nil
}

func (o *RestartOrchestrator) Run(ctx context.Context, options RestartOptions) (RestartResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	service := strings.TrimSpace(options.Service)
	if service == "" {
		return RestartResult{}, errors.New("restart service is required")
	}
	if _, err := validateComposeServices([]string{service}); err != nil {
		return RestartResult{}, fmt.Errorf("invalid restart service: %w", err)
	}

	if options.TimeoutProvided {
		if err := validateComposeTimeoutValue(strconv.Itoa(options.TimeoutSeconds)); err != nil {
			return RestartResult{}, err
		}
	}

	restartResult := RestartResult{
		Service:         service,
		TimeoutSeconds:  options.TimeoutSeconds,
		TimeoutProvided: options.TimeoutProvided,
	}

	o.logger.Info(
		"docker restart begin",
		"service", service,
		"timeout_provided", options.TimeoutProvided,
		"timeout_seconds", options.TimeoutSeconds,
	)

	restartResult.PreState = o.probeServiceState(ctx, service)
	o.logger.Debug(
		"docker restart pre-state snapshot",
		"service", service,
		"pre_checked", restartResult.PreState.Checked,
		"pre_status", restartResult.PreState.Status,
		"pre_reason", restartResult.PreState.Reason,
		"pre_stderr_summary", restartResult.PreState.StderrSummary,
		"pre_error", restartResult.PreState.Error,
	)

	args := make([]string, 0, 2)
	previewArgs := []string{"compose", "restart"}
	if options.TimeoutProvided {
		timeoutValue := strconv.Itoa(options.TimeoutSeconds)
		args = append(args, "--timeout", timeoutValue)
		previewArgs = append(previewArgs, "--timeout", timeoutValue)
	}
	previewArgs = append(previewArgs, service)

	o.logger.Debug(
		"docker restart invocation details",
		"service", service,
		"timeout_provided", options.TimeoutProvided,
		"timeout_seconds", options.TimeoutSeconds,
		"compose_preview", FormatComposeCommandPreview("docker", previewArgs),
	)

	restartExecResult, restartErr := o.compose.Run(ctx, ComposeCommand{
		Subcommand: "restart",
		Args:       args,
		Services:   []string{service},
	})
	restartResult.Restart = restartExecResult
	if restartErr != nil {
		o.logger.Error(
			"docker restart failed",
			"service", service,
			"timeout_provided", options.TimeoutProvided,
			"timeout_seconds", options.TimeoutSeconds,
			"elapsed", restartExecResult.Elapsed,
			"exit_status", restartExecResult.ExitCode,
			"stderr_summary", restartExecResult.StderrSummary,
			"error", execadapter.RedactText(restartErr.Error()),
		)
		return restartResult, restartErr
	}

	restartResult.PostState = o.probeServiceState(ctx, service)
	o.logger.Debug(
		"docker restart post-state snapshot",
		"service", service,
		"post_checked", restartResult.PostState.Checked,
		"post_status", restartResult.PostState.Status,
		"post_reason", restartResult.PostState.Reason,
		"post_stderr_summary", restartResult.PostState.StderrSummary,
		"post_error", restartResult.PostState.Error,
	)

	if restartResult.PostCheckDegraded() {
		o.logger.Warn(
			"docker restart completed with degraded post-check",
			"service", service,
			"timeout_provided", options.TimeoutProvided,
			"timeout_seconds", options.TimeoutSeconds,
			"elapsed", restartExecResult.Elapsed,
			"exit_status", restartExecResult.ExitCode,
			"pre_status", restartResult.PreState.Status,
			"pre_reason", restartResult.PreState.Reason,
			"post_status", restartResult.PostState.Status,
			"post_reason", restartResult.PostState.Reason,
		)
	} else {
		o.logger.Info(
			"docker restart end",
			"service", service,
			"timeout_provided", options.TimeoutProvided,
			"timeout_seconds", options.TimeoutSeconds,
			"elapsed", restartExecResult.Elapsed,
			"exit_status", restartExecResult.ExitCode,
			"pre_status", restartResult.PreState.Status,
			"pre_reason", restartResult.PreState.Reason,
			"post_status", restartResult.PostState.Status,
			"post_reason", restartResult.PostState.Reason,
		)
	}

	return restartResult, nil
}

func (o *RestartOrchestrator) probeServiceState(ctx context.Context, service string) RestartStateSnapshot {
	result, err := o.compose.Run(ctx, ComposeCommand{
		Subcommand: "ps",
		Args:       []string{"--all"},
		Services:   []string{service},
	})
	if err != nil {
		return RestartStateSnapshot{
			Checked:       true,
			Status:        ComposeServiceStateUnknown,
			Reason:        "compose_ps_failed",
			StderrSummary: joinComposeDiagnostics(err, result.StderrSummary),
			Error:         execadapter.RedactText(err.Error()),
		}
	}

	status, reason := classifyComposeServiceState(result.Stdout)
	return RestartStateSnapshot{
		Checked:       true,
		Status:        status,
		Reason:        reason,
		StderrSummary: strings.TrimSpace(result.StderrSummary),
	}
}

func classifyComposeServiceState(stdout string) (ComposeServiceState, string) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return ComposeServiceStateNotRunning, "compose_ps_empty_output"
	}

	lines := strings.Split(trimmed, "\n")
	serviceRows := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "name ") || strings.HasPrefix(lower, "name\t") {
			continue
		}
		if strings.HasPrefix(lower, "---") {
			continue
		}
		serviceRows = append(serviceRows, lower)
	}

	if len(serviceRows) == 0 {
		return ComposeServiceStateNotRunning, "compose_ps_no_service_rows"
	}

	hasRunning := false
	hasStopped := false
	for _, line := range serviceRows {
		lineHasRunning, lineHasStopped := classifyComposeServiceStateLine(line)
		hasRunning = hasRunning || lineHasRunning
		hasStopped = hasStopped || lineHasStopped
	}

	if hasRunning && !hasStopped {
		return ComposeServiceStateRunning, "compose_ps_running"
	}
	if hasRunning && hasStopped {
		return ComposeServiceStateUnknown, "compose_ps_mixed_container_states"
	}
	if hasStopped {
		return ComposeServiceStateNotRunning, "compose_ps_not_running"
	}

	return ComposeServiceStateUnknown, "compose_ps_unclassified"
}

func classifyComposeServiceStateLine(line string) (bool, bool) {
	hasRunning := strings.Contains(line, " up ") ||
		strings.Contains(line, "\tup ") ||
		strings.HasSuffix(line, " up") ||
		strings.Contains(line, " running")

	hasStopped := strings.Contains(line, " exited") ||
		strings.HasPrefix(line, "exited ") ||
		strings.Contains(line, "\texited ") ||
		strings.Contains(line, " dead") ||
		strings.HasPrefix(line, "dead ") ||
		strings.Contains(line, "\tdead ") ||
		strings.Contains(line, " created") ||
		strings.HasPrefix(line, "created ") ||
		strings.Contains(line, "\tcreated ") ||
		strings.Contains(line, " restarting") ||
		strings.HasPrefix(line, "restarting ") ||
		strings.Contains(line, "\trestarting ") ||
		strings.Contains(line, " stopped") ||
		strings.HasPrefix(line, "stopped ") ||
		strings.Contains(line, "\tstopped ") ||
		strings.Contains(line, " removing") ||
		strings.HasPrefix(line, "removing ") ||
		strings.Contains(line, "\tremoving ") ||
		strings.Contains(line, " paused") ||
		strings.HasPrefix(line, "paused ") ||
		strings.Contains(line, "\tpaused ")

	return hasRunning, hasStopped
}

func joinComposeDiagnostics(runErr error, stderrSummary string) string {
	parts := make([]string, 0, 2)
	seen := map[string]struct{}{}
	appendPart := func(value string) {
		trimmed := strings.TrimSpace(execadapter.RedactText(value))
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		parts = append(parts, trimmed)
	}

	if runErr != nil {
		appendPart(runErr.Error())
	}
	appendPart(stderrSummary)
	return strings.Join(parts, " | ")
}
