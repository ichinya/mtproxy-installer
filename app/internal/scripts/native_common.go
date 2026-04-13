package scripts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

var trustedDockerBinaryCandidates = []string{
	"/usr/bin/docker",
	"/usr/local/bin/docker",
	"/bin/docker",
}

const (
	nativeLifecycleCommand     = "native-go-lifecycle"
	nativeStderrSummaryLimit   = 320
	defaultTelemtImageSource   = "whn0thacked/telemt-docker:latest"
	defaultMTGImageSource      = "nineseconds/mtg:2"
	defaultTelemtAPIPort       = 9091
	defaultTelemtTLSDomain     = "www.wikipedia.org"
	defaultTelemtProxyUser     = "main"
	defaultInstallPollAttempts = 30
	defaultInstallPollDelay    = 2 * time.Second
	telemtDataOwnerUID         = 65532
	telemtDataOwnerGID         = 65532
)

type lifecycleTranscript struct {
	startedAt time.Time
	stdout    bytes.Buffer
	stderr    bytes.Buffer
}

func newLifecycleTranscript() *lifecycleTranscript {
	return &lifecycleTranscript{
		startedAt: time.Now(),
	}
}

func (t *lifecycleTranscript) stdoutLine(line string) {
	t.writeLine(&t.stdout, line)
}

func (t *lifecycleTranscript) stderrLine(line string) {
	t.writeLine(&t.stderr, line)
}

func (t *lifecycleTranscript) writeLine(target *bytes.Buffer, line string) {
	if target == nil {
		return
	}
	target.WriteString(line)
	target.WriteByte('\n')
}

func (t *lifecycleTranscript) appendResult(result execadapter.Result) {
	if strings.TrimSpace(result.Stdout) != "" {
		t.stdout.WriteString(result.Stdout)
		if !strings.HasSuffix(result.Stdout, "\n") {
			t.stdout.WriteByte('\n')
		}
	}
	if strings.TrimSpace(result.Stderr) != "" {
		t.stderr.WriteString(result.Stderr)
		if !strings.HasSuffix(result.Stderr, "\n") {
			t.stderr.WriteByte('\n')
		}
	}
}

func (t *lifecycleTranscript) result(args []string, exitCode int) execadapter.Result {
	stdout := t.stdout.String()
	stderr := t.stderr.String()
	return execadapter.Result{
		Command:       nativeLifecycleCommand,
		Args:          append([]string(nil), args...),
		RedactedArgs:  execadapter.RedactArgs(args),
		Stdout:        stdout,
		Stderr:        stderr,
		StderrSummary: execadapter.SummarizeStderr(stderr, nativeStderrSummaryLimit),
		ExitCode:      exitCode,
		StartedAt:     t.startedAt,
		Elapsed:       time.Since(t.startedAt),
	}
}

func lifecycleCommandError(result execadapter.Result, err error) error {
	if err == nil {
		return nil
	}
	var commandErr *execadapter.CommandError
	if errors.As(err, &commandErr) {
		return err
	}
	return &execadapter.CommandError{
		Result: result,
		Err:    err,
	}
}

func requirePrivilegedLifecycleExecution(commandName string) error {
	uid, ok := currentUserUID()
	if !ok {
		return nil
	}
	if uid == 0 {
		return nil
	}
	return fmt.Errorf("%s lifecycle requires root privileges", commandName)
}

func (m *Manager) resolveDockerPath() (string, error) {
	return resolveTrustedBinaryPath("docker", m.dockerPath, trustedDockerBinaryCandidates)
}

func (m *Manager) runDockerCommand(
	ctx context.Context,
	dockerPath string,
	workingDir string,
	envOverrides map[string]string,
	args ...string,
) (execadapter.Result, error) {
	request := execadapter.Request{
		Command:     dockerPath,
		Args:        append([]string(nil), args...),
		WorkingDir:  workingDir,
		UseSafePath: true,
	}

	dockerEnv := filterDockerEnvOverrides(envOverrides)
	if len(dockerEnv) > 0 {
		request.EnvOverrides = dockerEnv
		request.AllowedEnvKeys = sortedKeys(dockerEnv)
	}

	return m.runner.Run(ctx, request)
}

func (m *Manager) runComposeCommand(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
	args ...string,
) (execadapter.Result, error) {
	if runtimeState == nil {
		return execadapter.Result{}, errors.New("runtime installation is required for compose command")
	}

	composeArgs := []string{
		"compose",
		"-f", runtimeState.Paths.ComposeFile,
		"--project-directory", runtimeState.Paths.InstallDir,
		"--env-file", runtimeState.Paths.EnvFile,
	}
	composeArgs = append(composeArgs, args...)

	return m.runDockerCommand(ctx, dockerPath, runtimeState.Paths.InstallDir, envOverrides, composeArgs...)
}

func filterDockerEnvOverrides(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	filtered := make(map[string]string)
	for key, value := range values {
		if _, ok := trustBoundaryEnvOverrideKeyAllowlist[key]; ok {
			filtered[key] = value
			continue
		}
		for _, prefix := range trustBoundaryEnvOverridePrefixAllowlist {
			if strings.HasPrefix(key, prefix) {
				filtered[key] = value
				break
			}
		}
	}

	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func backupFileIfExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("backup target is a directory: %s", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	backupPath := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	return os.WriteFile(backupPath, content, info.Mode().Perm())
}

func writeFileAtomically(path string, content string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()

	if _, err := tempFile.WriteString(content); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := tempFile.Chmod(mode); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func upsertEnvFileValue(path string, key string, value string) error {
	lines := make([]string, 0, 16)
	if raw, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	replacement := fmt.Sprintf("%s=%s", key, value)
	updated := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if !strings.HasPrefix(trimmed, key+"=") {
			continue
		}
		lines[index] = replacement
		updated = true
	}
	if !updated {
		lines = append(lines, replacement)
	}

	content := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	return writeFileAtomically(path, content, 0o644)
}

func firstNonEmptyLine(raw string) string {
	for _, line := range splitLifecycleLines(raw) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
