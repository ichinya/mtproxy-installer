package scripts

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
)

var currentLifecycleGOOS = func() string {
	return goruntime.GOOS
}

type lifecyclePreflightError struct {
	Command string
	Check   string
	Problem string
	Remedy  string
	Cause   error
}

func (e *lifecyclePreflightError) Error() string {
	if e == nil {
		return "lifecycle preflight failed"
	}

	parts := make([]string, 0, 3)
	if strings.TrimSpace(e.Command) != "" {
		parts = append(parts, fmt.Sprintf("%s preflight failed", e.Command))
	} else {
		parts = append(parts, "lifecycle preflight failed")
	}
	if strings.TrimSpace(e.Check) != "" {
		parts = append(parts, fmt.Sprintf("check=%s", e.Check))
	}
	if strings.TrimSpace(e.Problem) != "" {
		parts = append(parts, e.Problem)
	}

	message := strings.Join(parts, ": ")
	if strings.TrimSpace(e.Remedy) != "" {
		message += ". Fix: " + e.Remedy
	}
	return message
}

func (e *lifecyclePreflightError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newLifecyclePreflightError(command string, check string, problem string, remedy string, cause error) error {
	return &lifecyclePreflightError{
		Command: strings.TrimSpace(command),
		Check:   strings.TrimSpace(check),
		Problem: strings.TrimSpace(problem),
		Remedy:  strings.TrimSpace(remedy),
		Cause:   cause,
	}
}

func writeLifecycleFailure(transcript *lifecycleTranscript, err error) {
	if transcript == nil || err == nil {
		return
	}

	transcript.stderrLine("Error: " + execadapter.RedactText(err.Error()))

	var preflightErr *lifecyclePreflightError
	if errors.As(err, &preflightErr) && strings.TrimSpace(preflightErr.Remedy) != "" {
		transcript.stderrLine("Hint: " + execadapter.RedactText(preflightErr.Remedy))
	}
}

func (m *Manager) runLifecyclePreflight(
	ctx context.Context,
	commandName string,
	installDir string,
	envOverrides map[string]string,
) (string, error) {
	if err := ensureLifecycleHostOS(commandName); err != nil {
		return "", err
	}
	goos := currentLifecycleGOOS()

	if err := requirePrivilegedLifecycleExecution(commandName); err != nil {
		return "", newLifecyclePreflightError(
			commandName,
			"permissions",
			"current user is not root",
			fmt.Sprintf("rerun with sudo, for example: sudo ./mtproxy %s ...", commandName),
			err,
		)
	}

	dockerPath, err := m.resolveDockerPath()
	if err != nil {
		return "", newLifecyclePreflightError(
			commandName,
			"docker_binary",
			"trusted docker binary is unavailable",
			"install Docker Engine and ensure docker is available at /usr/bin/docker or /usr/local/bin/docker",
			err,
		)
	}

	_, err = m.runDockerCommand(ctx, dockerPath, m.repoRoot, envOverrides, "compose", "version")
	if err != nil {
		return "", newLifecyclePreflightError(
			commandName,
			"docker_compose",
			"docker compose plugin is unavailable",
			"install Docker Compose plugin, for example on Debian/Ubuntu: sudo apt-get install docker-compose-plugin",
			err,
		)
	}

	_, err = m.runDockerCommand(ctx, dockerPath, m.repoRoot, envOverrides, "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		return "", newLifecyclePreflightError(
			commandName,
			"docker_daemon",
			"docker daemon is unavailable or inaccessible",
			remedyForDockerDaemonFailure(err),
			err,
		)
	}

	m.logger.Debug(
		"lifecycle preflight passed",
		"command", commandName,
		"install_dir", installDir,
		"goos", goos,
		"docker_path", dockerPath,
	)

	return dockerPath, nil
}

func ensureLifecycleHostOS(commandName string) error {
	goos := currentLifecycleGOOS()
	if goos == "linux" {
		return nil
	}

	return newLifecyclePreflightError(
		commandName,
		"os",
		fmt.Sprintf("GOOS=%s is unsupported for runtime lifecycle commands", goos),
		remedyForLifecycleOS(goos, commandName),
		nil,
	)
}

func remedyForLifecycleOS(goos string, commandName string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return fmt.Sprintf("run `%s` inside Linux or WSL where Docker Compose runtime paths like /opt/mtproxy-installer are valid", commandName)
	case "darwin":
		return fmt.Sprintf("run `%s` against a Linux host or inside a Linux VM/WSL environment", commandName)
	default:
		return fmt.Sprintf("run `%s` in a Linux environment with Docker Compose support", commandName)
	}
}

func remedyForDockerDaemonFailure(err error) string {
	message := strings.ToLower(strings.TrimSpace(errorTextOrEmpty(err)))

	switch {
	case strings.Contains(message, "permission denied"):
		return "ensure the current user can access the Docker daemon socket, or rerun with sudo"
	case strings.Contains(message, "cannot connect"):
		return "start Docker daemon/service and verify that DOCKER_HOST or DOCKER_CONTEXT is correct"
	case strings.Contains(message, "is the docker daemon running"):
		return "start Docker daemon/service and rerun the command"
	case strings.Contains(message, "docker host"):
		return "verify DOCKER_HOST, DOCKER_CONTEXT and Docker TLS settings before rerunning"
	default:
		return "start Docker, verify daemon connectivity with `docker info`, then rerun the command"
	}
}

func errorTextOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
