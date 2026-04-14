package scripts

import (
	"strings"
	"testing"
)

func TestLifecyclePreflightErrorIncludesFix(t *testing.T) {
	t.Parallel()

	err := newLifecyclePreflightError(
		"install",
		"os",
		"GOOS=windows is unsupported for runtime lifecycle commands",
		"run `install` inside Linux or WSL where Docker Compose runtime paths like /opt/mtproxy-installer are valid",
		nil,
	)
	if err == nil {
		t.Fatalf("expected preflight error")
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Fatalf("expected remediation hint in error, got %q", err.Error())
	}
}

func TestWriteLifecycleFailureWritesHintForPreflightErrors(t *testing.T) {
	t.Parallel()

	transcript := newLifecycleTranscript()
	writeLifecycleFailure(transcript, newLifecyclePreflightError(
		"update",
		"docker_daemon",
		"docker daemon is unavailable or inaccessible",
		"start Docker daemon/service and rerun the command",
		nil,
	))

	result := transcript.result([]string{"update"}, 1)
	if !strings.Contains(result.Stderr, "Hint: start Docker daemon/service and rerun the command") {
		t.Fatalf("expected remediation hint in stderr, got %q", result.Stderr)
	}
}

func TestRemedyForDockerDaemonFailureHandlesPermissionDenied(t *testing.T) {
	t.Parallel()

	remedy := remedyForDockerDaemonFailure(assertErr("permission denied while trying to connect to the Docker daemon socket"))
	if !strings.Contains(remedy, "sudo") && !strings.Contains(remedy, "access") {
		t.Fatalf("unexpected remedy: %q", remedy)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}
