package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
)

type LogsOptions struct {
	Service    string
	Tail       string
	Follow     bool
	Timestamps bool
	NoColor    bool
	Stdout     io.Writer
	Stderr     io.Writer
}

type LogsRunnerOptions struct {
	Compose composeLogsRunner
	Logger  *slog.Logger
}

type LogsRunner struct {
	compose composeLogsRunner
	logger  *slog.Logger
}

type composeLogsRunner interface {
	Run(context.Context, ComposeCommand) (execadapter.Result, error)
}

func NewLogsRunner(options LogsRunnerOptions) (*LogsRunner, error) {
	if options.Compose == nil {
		return nil, errors.New("compose runner is required for logs command")
	}

	return &LogsRunner{
		compose: options.Compose,
		logger:  fallbackLogger(options.Logger),
	}, nil
}

func (r *LogsRunner) Run(ctx context.Context, options LogsOptions) (execadapter.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	service := strings.TrimSpace(options.Service)
	if service == "" {
		return execadapter.Result{}, errors.New("logs service is required")
	}
	if _, err := validateComposeServices([]string{service}); err != nil {
		return execadapter.Result{}, fmt.Errorf("invalid logs service: %w", err)
	}

	tail := strings.TrimSpace(options.Tail)
	if tail == "" {
		tail = "all"
	}
	if err := validateComposeTailValue(tail); err != nil {
		return execadapter.Result{}, err
	}

	args := make([]string, 0, 6)
	if options.Follow {
		args = append(args, "--follow")
	}
	if options.Timestamps {
		args = append(args, "--timestamps")
	}
	if options.NoColor {
		args = append(args, "--no-color")
	}
	args = append(args, "--tail", tail)

	previewArgs := []string{"compose", "logs"}
	previewArgs = append(previewArgs, args...)
	previewArgs = append(previewArgs, service)

	r.logger.Info(
		"docker logs start",
		"service", service,
		"follow", options.Follow,
		"tail", tail,
		"timestamps", options.Timestamps,
		"no_color", options.NoColor,
	)
	r.logger.Debug(
		"docker logs invocation details",
		"service", service,
		"follow", options.Follow,
		"tail", tail,
		"timestamps", options.Timestamps,
		"no_color", options.NoColor,
		"compose_preview", FormatComposeCommandPreview("docker", previewArgs),
		"stdout_streaming", options.Stdout != nil,
		"stderr_streaming", options.Stderr != nil,
	)

	result, err := r.compose.Run(ctx, ComposeCommand{
		Subcommand:              "logs",
		Args:                    args,
		Services:                []string{service},
		Stdout:                  options.Stdout,
		Stderr:                  options.Stderr,
		DisableStdoutCapture:    options.Follow || options.Stdout != nil,
		DisableStderrCapture:    true,
		DisableStderrSummaryLog: true,
	})
	if err != nil {
		r.logger.Error(
			"docker logs failed",
			"service", service,
			"follow", options.Follow,
			"tail", tail,
			"timestamps", options.Timestamps,
			"no_color", options.NoColor,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"error", execadapter.RedactText(err.Error()),
		)
		return result, err
	}

	r.logger.Info(
		"docker logs finish",
		"service", service,
		"follow", options.Follow,
		"tail", tail,
		"timestamps", options.Timestamps,
		"no_color", options.NoColor,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
	)

	return result, nil
}
