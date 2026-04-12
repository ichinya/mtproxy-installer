package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"mtproxy-installer/app/internal/docker"
	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

func TestParseLogsCommandArgs(t *testing.T) {
	options, err := parseLogsCommandArgs([]string{
		"--follow",
		"--tail",
		"40",
		"--timestamps",
		"--no-color",
		"telemt",
	})
	if err != nil {
		t.Fatalf("expected logs args parse success, got %v", err)
	}

	if options.service != "telemt" {
		t.Fatalf("expected telemt service, got %q", options.service)
	}
	if options.tail != "40" {
		t.Fatalf("expected tail=40, got %q", options.tail)
	}
	if !options.follow {
		t.Fatalf("expected follow=true")
	}
	if !options.timestamps {
		t.Fatalf("expected timestamps=true")
	}
	if !options.noColor {
		t.Fatalf("expected noColor=true")
	}
}

func TestRunLogsResolvesDefaultServiceFromProvider(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	capture := &capturingCLIComposeRunner{}
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return capture, nil
	}

	ctx, stdout, logs := newTestCommandContext("logs", "--tail", "20")
	if err := runLogs(ctx); err != nil {
		t.Fatalf("expected logs command success, got %v", err)
	}

	if len(capture.lastCommand.Services) != 1 || capture.lastCommand.Services[0] != "mtg" {
		t.Fatalf("expected default mtg service, got %+v", capture.lastCommand.Services)
	}
	assertContainsToken(t, capture.lastCommand.Args, "--tail")
	assertContainsToken(t, capture.lastCommand.Args, "20")
	if !capture.lastCommand.DisableStdoutCapture {
		t.Fatalf("expected stdout capture to be disabled for streaming logs command output")
	}

	if !strings.Contains(stdout.String(), "streamed line") {
		t.Fatalf("expected streamed command output, got %q", stdout.String())
	}
	if !strings.Contains(logs.String(), "default_service_used=true") {
		t.Fatalf("expected default service resolution debug log, got: %s", logs.String())
	}
}

func TestRunLogsRejectsMultipleServices(t *testing.T) {
	ctx, _, _ := newTestCommandContext("logs", "telemt", "mtg")
	err := runLogs(ctx)
	if err == nil {
		t.Fatalf("expected logs command to reject multiple services")
	}
}

func TestRunLogsDoesNotLogStderrSummaryOnFailure(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	capture := &capturingCLIComposeRunner{
		result: execadapter.Result{
			ExitCode:      1,
			Elapsed:       time.Second,
			StderrSummary: "raw container stderr line",
		},
		err: errors.New("compose logs failed"),
	}
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return capture, nil
	}

	ctx, _, logs := newTestCommandContext("logs", "--tail", "20")
	err := runLogs(ctx)
	if err == nil {
		t.Fatalf("expected logs command failure")
	}

	logText := logs.String()
	if !strings.Contains(logText, "logs command failed") {
		t.Fatalf("expected logs failure marker, got: %s", logText)
	}
	if strings.Contains(logText, "stderr_summary") {
		t.Fatalf("expected logs path to avoid stderr_summary field, got: %s", logText)
	}
	if strings.Contains(logText, "raw container stderr line") {
		t.Fatalf("expected raw stderr summary to stay out of structured logs, got: %s", logText)
	}
}

type capturingCLIComposeRunner struct {
	lastCommand docker.ComposeCommand
	result      execadapter.Result
	err         error
}

func (s *capturingCLIComposeRunner) Run(_ context.Context, command docker.ComposeCommand) (execadapter.Result, error) {
	s.lastCommand = command
	if command.Stdout != nil {
		_, _ = io.WriteString(command.Stdout, "streamed line\n")
	}
	if s.err != nil {
		return s.result, s.err
	}
	if s.result.ExitCode == 0 {
		return execadapter.Result{ExitCode: 0}, nil
	}
	return s.result, nil
}

func assertContainsToken(t *testing.T, values []string, token string) {
	t.Helper()
	for _, value := range values {
		if value == token {
			return
		}
	}
	t.Fatalf("expected token %q in %#v", token, values)
}
