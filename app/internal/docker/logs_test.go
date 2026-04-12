package docker

import (
	"bytes"
	"context"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
)

func TestLogsRunnerRunBuildsComposeCommandWithStreaming(t *testing.T) {
	stub := &captureComposeLogsRunner{
		result: execadapter.Result{
			ExitCode: 0,
		},
	}
	runner, err := NewLogsRunner(LogsRunnerOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected logs runner to initialize, got %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	_, err = runner.Run(context.Background(), LogsOptions{
		Service:    "telemt",
		Tail:       "25",
		Follow:     true,
		Timestamps: true,
		NoColor:    true,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("expected logs runner to succeed, got %v", err)
	}

	if stub.command.Subcommand != "logs" {
		t.Fatalf("expected logs subcommand, got %q", stub.command.Subcommand)
	}
	if len(stub.command.Services) != 1 || stub.command.Services[0] != "telemt" {
		t.Fatalf("expected telemt service, got %+v", stub.command.Services)
	}
	assertContainsToken(t, stub.command.Args, "--follow")
	assertContainsToken(t, stub.command.Args, "--timestamps")
	assertContainsToken(t, stub.command.Args, "--no-color")
	assertContainsToken(t, stub.command.Args, "--tail")
	assertContainsToken(t, stub.command.Args, "25")
	if !stub.command.DisableStdoutCapture {
		t.Fatalf("expected follow mode to disable stdout capture")
	}
	if !stub.command.DisableStderrCapture {
		t.Fatalf("expected logs path to disable stderr capture")
	}
	if !stub.command.DisableStderrSummaryLog {
		t.Fatalf("expected logs path to suppress stderr_summary logging")
	}
	if stub.command.Stdout == nil {
		t.Fatalf("expected stdout streaming writer to be set")
	}
	if stub.command.Stderr == nil {
		t.Fatalf("expected stderr streaming writer to be set")
	}
}

func TestLogsRunnerRunDisablesStderrCaptureWithoutFollow(t *testing.T) {
	stub := &captureComposeLogsRunner{
		result: execadapter.Result{
			ExitCode: 0,
		},
	}
	runner, err := NewLogsRunner(LogsRunnerOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected logs runner to initialize, got %v", err)
	}

	_, err = runner.Run(context.Background(), LogsOptions{
		Service: "telemt",
		Tail:    "10",
	})
	if err != nil {
		t.Fatalf("expected logs runner to succeed, got %v", err)
	}

	if stub.command.DisableStdoutCapture {
		t.Fatalf("expected stdout capture to stay enabled without follow")
	}
	if !stub.command.DisableStderrCapture {
		t.Fatalf("expected stderr capture to stay disabled for logs path")
	}
	if !stub.command.DisableStderrSummaryLog {
		t.Fatalf("expected stderr_summary logging to be disabled for logs path")
	}
}

func TestLogsRunnerRunDisablesStdoutCaptureWhenStreamingWithoutFollow(t *testing.T) {
	stub := &captureComposeLogsRunner{
		result: execadapter.Result{
			ExitCode: 0,
		},
	}
	runner, err := NewLogsRunner(LogsRunnerOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected logs runner to initialize, got %v", err)
	}

	var stdout bytes.Buffer
	_, err = runner.Run(context.Background(), LogsOptions{
		Service: "telemt",
		Tail:    "10",
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("expected logs runner to succeed, got %v", err)
	}

	if !stub.command.DisableStdoutCapture {
		t.Fatalf("expected stdout capture to be disabled when streaming to stdout writer")
	}
}

func TestLogsRunnerRejectsInvalidTail(t *testing.T) {
	stub := &captureComposeLogsRunner{}
	runner, err := NewLogsRunner(LogsRunnerOptions{
		Compose: stub,
	})
	if err != nil {
		t.Fatalf("expected logs runner to initialize, got %v", err)
	}

	_, err = runner.Run(context.Background(), LogsOptions{
		Service: "telemt",
		Tail:    "-1",
	})
	if err == nil {
		t.Fatalf("expected invalid tail value to fail")
	}
}

type captureComposeLogsRunner struct {
	command ComposeCommand
	result  execadapter.Result
	err     error
}

func (s *captureComposeLogsRunner) Run(_ context.Context, command ComposeCommand) (execadapter.Result, error) {
	s.command = command
	return s.result, s.err
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
