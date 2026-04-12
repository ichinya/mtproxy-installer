package docker

import (
	"context"
	"errors"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
)

func TestRestartOrchestratorDetectsDegradedPostState(t *testing.T) {
	stub := &queueComposeRestartRunner{
		responses: []composeRestartResponse{
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\ntelemt image Up 1 minute",
				},
			},
			{
				result: execadapter.Result{
					ExitCode: 0,
				},
			},
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\ntelemt image Exited (1) 2 seconds ago",
				},
			},
		},
	}
	orchestrator, err := NewRestartOrchestrator(RestartOrchestratorOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected restart orchestrator init success, got %v", err)
	}

	result, err := orchestrator.Run(context.Background(), RestartOptions{
		Service:         "telemt",
		TimeoutProvided: true,
		TimeoutSeconds:  15,
	})
	if err != nil {
		t.Fatalf("expected restart to succeed, got %v", err)
	}

	if len(stub.calls) != 3 {
		t.Fatalf("expected 3 compose calls, got %d", len(stub.calls))
	}
	if stub.calls[0].Subcommand != "ps" {
		t.Fatalf("expected first call to be ps, got %q", stub.calls[0].Subcommand)
	}
	if len(stub.calls[0].Args) != 1 || stub.calls[0].Args[0] != "--all" {
		t.Fatalf("expected first ps call to use --all, got %#v", stub.calls[0].Args)
	}
	if stub.calls[1].Subcommand != "restart" {
		t.Fatalf("expected second call to be restart, got %q", stub.calls[1].Subcommand)
	}
	if stub.calls[2].Subcommand != "ps" {
		t.Fatalf("expected third call to be ps, got %q", stub.calls[2].Subcommand)
	}
	if len(stub.calls[2].Args) != 1 || stub.calls[2].Args[0] != "--all" {
		t.Fatalf("expected third ps call to use --all, got %#v", stub.calls[2].Args)
	}
	if len(stub.calls[1].Args) != 2 || stub.calls[1].Args[0] != "--timeout" || stub.calls[1].Args[1] != "15" {
		t.Fatalf("unexpected restart args: %#v", stub.calls[1].Args)
	}
	if result.PreState.Status != ComposeServiceStateRunning {
		t.Fatalf("expected pre-state running, got %s", result.PreState.Status)
	}
	if result.PostState.Status != ComposeServiceStateNotRunning {
		t.Fatalf("expected post-state not_running, got %s", result.PostState.Status)
	}
	if !result.PostCheckDegraded() {
		t.Fatalf("expected degraded post-check")
	}
}

func TestRestartOrchestratorTreatsMixedPostStateAsDegraded(t *testing.T) {
	stub := &queueComposeRestartRunner{
		responses: []composeRestartResponse{
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\ntelemt image Up 1 minute",
				},
			},
			{
				result: execadapter.Result{
					ExitCode: 0,
				},
			},
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\ntelemt image Up 2 seconds\ntelemt-worker image Exited (1) 1 second ago",
				},
			},
		},
	}
	orchestrator, err := NewRestartOrchestrator(RestartOrchestratorOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected restart orchestrator init success, got %v", err)
	}

	result, err := orchestrator.Run(context.Background(), RestartOptions{
		Service: "telemt",
	})
	if err != nil {
		t.Fatalf("expected restart to succeed, got %v", err)
	}

	if result.PostState.Status != ComposeServiceStateUnknown {
		t.Fatalf("expected post-state unknown for mixed compose state, got %s", result.PostState.Status)
	}
	if result.PostState.Reason != "compose_ps_mixed_container_states" {
		t.Fatalf("expected mixed-state reason, got %q", result.PostState.Reason)
	}
	if !result.PostCheckDegraded() {
		t.Fatalf("expected mixed-state post-check to be degraded")
	}
}

func TestRestartOrchestratorReturnsErrorOnRestartFailure(t *testing.T) {
	stub := &queueComposeRestartRunner{
		responses: []composeRestartResponse{
			{
				result: execadapter.Result{
					Stdout: "NAME IMAGE STATUS\ntelemt image Up 1 minute",
				},
			},
			{
				result: execadapter.Result{
					ExitCode:      1,
					StderrSummary: "permission denied",
				},
				err: errors.New("restart failed"),
			},
		},
	}
	orchestrator, err := NewRestartOrchestrator(RestartOrchestratorOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected restart orchestrator init success, got %v", err)
	}

	result, err := orchestrator.Run(context.Background(), RestartOptions{
		Service: "telemt",
	})
	if err == nil {
		t.Fatalf("expected restart failure")
	}
	if len(stub.calls) != 2 {
		t.Fatalf("expected restart flow to stop after failing restart call, got %d calls", len(stub.calls))
	}
	if !result.PreState.Checked {
		t.Fatalf("expected pre-state to be captured")
	}
	if result.PostState.Checked {
		t.Fatalf("expected post-state to stay unchecked on restart failure")
	}
}

func TestClassifyComposeServiceStateDetectsExitedFromPSAllOutput(t *testing.T) {
	status, reason := classifyComposeServiceState(`
NAME                IMAGE             COMMAND            SERVICE   CREATED         STATUS                        PORTS
mtproxy-telemt-1    telemt/telemt     "/usr/bin/telemt" telemt    2 minutes ago   Exited (1) 5 seconds ago
`)
	if status != ComposeServiceStateNotRunning {
		t.Fatalf("expected not_running for Exited status, got %s", status)
	}
	if reason != "compose_ps_not_running" {
		t.Fatalf("expected compose_ps_not_running reason, got %q", reason)
	}
}

func TestClassifyComposeServiceStateDetectsMixedFromPSAllOutput(t *testing.T) {
	status, reason := classifyComposeServiceState(`
NAME                   IMAGE             COMMAND            SERVICE   CREATED         STATUS                        PORTS
mtproxy-telemt-1       telemt/telemt     "/usr/bin/telemt" telemt    2 minutes ago   Up 20 seconds                 0.0.0.0:443->443/tcp
mtproxy-telemt-old-1   telemt/telemt     "/usr/bin/telemt" telemt    3 minutes ago   Exited (1) 10 seconds ago
`)
	if status != ComposeServiceStateUnknown {
		t.Fatalf("expected unknown for mixed running/exited statuses, got %s", status)
	}
	if reason != "compose_ps_mixed_container_states" {
		t.Fatalf("expected compose_ps_mixed_container_states reason, got %q", reason)
	}
}

type composeRestartResponse struct {
	result execadapter.Result
	err    error
}

type queueComposeRestartRunner struct {
	responses []composeRestartResponse
	calls     []ComposeCommand
	index     int
}

func (s *queueComposeRestartRunner) Run(_ context.Context, command ComposeCommand) (execadapter.Result, error) {
	s.calls = append(s.calls, command)
	if s.index >= len(s.responses) {
		return execadapter.Result{}, errors.New("unexpected compose call")
	}
	response := s.responses[s.index]
	s.index++
	return response.result, response.err
}
