package cli

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"mtproxy-installer/app/internal/docker"
	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

func TestParseRestartCommandArgs(t *testing.T) {
	options, err := parseRestartCommandArgs([]string{"--timeout", "15", "telemt"})
	if err != nil {
		t.Fatalf("expected restart args parse success, got %v", err)
	}

	if options.service != "telemt" {
		t.Fatalf("expected telemt service, got %q", options.service)
	}
	if !options.timeoutProvided {
		t.Fatalf("expected timeout to be marked as provided")
	}
	if options.timeoutSeconds != 15 {
		t.Fatalf("expected timeout=15, got %d", options.timeoutSeconds)
	}
}

func TestRunRestartWarnsWhenPostCheckIsDegraded(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	queue := &queueCLIComposeRunner{
		responses: []queueCLIComposeResponse{
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\nmtg image Up 1 minute",
				},
			},
			{
				result: execadapter.Result{
					ExitCode: 0,
				},
			},
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\nmtg image Exited (1) 1 second ago",
				},
			},
		},
	}
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return queue, nil
	}

	ctx, stdout, logs := newTestCommandContext("restart", "--timeout", "10")
	if err := runRestart(ctx); err != nil {
		t.Fatalf("expected restart command success with degraded warning, got %v", err)
	}

	if len(queue.calls) != 3 {
		t.Fatalf("expected 3 compose calls, got %d", len(queue.calls))
	}
	if queue.calls[0].Subcommand != "ps" || queue.calls[1].Subcommand != "restart" || queue.calls[2].Subcommand != "ps" {
		t.Fatalf("unexpected compose call order: %#v", queue.calls)
	}
	if len(queue.calls[0].Args) != 1 || queue.calls[0].Args[0] != "--all" {
		t.Fatalf("expected pre-check ps call to use --all, got %#v", queue.calls[0].Args)
	}
	if len(queue.calls[2].Args) != 1 || queue.calls[2].Args[0] != "--all" {
		t.Fatalf("expected post-check ps call to use --all, got %#v", queue.calls[2].Args)
	}
	if len(queue.calls[1].Services) != 1 || queue.calls[1].Services[0] != "mtg" {
		t.Fatalf("expected default mtg service for restart command, got %+v", queue.calls[1].Services)
	}
	if !strings.Contains(stdout.String(), "Warning: restart completed with degradation") {
		t.Fatalf("expected operator-facing degraded warning, got: %s", stdout.String())
	}
	logText := logs.String()
	if !strings.Contains(logText, "restart command completed with degraded post-check") {
		t.Fatalf("expected degraded post-check warning log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Fatalf("expected WARN log level, got: %s", logText)
	}
}

func TestRunRestartWarnsWhenPostCheckIsMixedState(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	queue := &queueCLIComposeRunner{
		responses: []queueCLIComposeResponse{
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\nmtg image Up 1 minute",
				},
			},
			{
				result: execadapter.Result{
					ExitCode: 0,
				},
			},
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\nmtg image Up 5 seconds\nmtg-worker image Exited (1) 3 seconds ago",
				},
			},
		},
	}
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return queue, nil
	}

	ctx, stdout, logs := newTestCommandContext("restart")
	if err := runRestart(ctx); err != nil {
		t.Fatalf("expected restart command success with mixed-state warning, got %v", err)
	}

	if !strings.Contains(stdout.String(), "compose_ps_mixed_container_states") {
		t.Fatalf("expected mixed-state marker in operator summary, got: %s", stdout.String())
	}
	logText := logs.String()
	if !strings.Contains(logText, "restart command completed with degraded post-check") {
		t.Fatalf("expected degraded warning log, got: %s", logText)
	}
	if !strings.Contains(logText, "post_status=unknown") {
		t.Fatalf("expected mixed-state post_status=unknown, got: %s", logText)
	}
}

func TestRunRestartRejectsInvalidTimeout(t *testing.T) {
	ctx, _, _ := newTestCommandContext("restart", "--timeout", "-1")
	err := runRestart(ctx)
	if err == nil {
		t.Fatalf("expected invalid timeout to fail")
	}
}

type queueCLIComposeResponse struct {
	result execadapter.Result
	err    error
}

type queueCLIComposeRunner struct {
	responses []queueCLIComposeResponse
	calls     []docker.ComposeCommand
	index     int
}

func (s *queueCLIComposeRunner) Run(_ context.Context, command docker.ComposeCommand) (execadapter.Result, error) {
	s.calls = append(s.calls, command)
	if s.index >= len(s.responses) {
		return execadapter.Result{}, errors.New("unexpected compose call")
	}
	response := s.responses[s.index]
	s.index++
	return response.result, response.err
}
