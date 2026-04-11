package cli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mtproxy-installer/app/internal/docker"
	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

func TestRunStatusRendersTelemtSummaryWithRedactedLink(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	link := "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{}, nil
	}
	newTelemtAPI = func(*runtime.RuntimeInstallation, *slog.Logger) (telemtStatusAPI, error) {
		return &stubTelemtStatusAPI{endpoint: "http://127.0.0.1:9091"}, nil
	}
	collectTelemtStatus = func(context.Context, telemt.StatusCollectorOptions) (telemt.StatusSummary, error) {
		return telemt.StatusSummary{
			Provider:        runtime.ProviderTelemt,
			InstallDir:      "/opt/mtproxy-installer",
			ControlEndpoint: "http://127.0.0.1:9091",
			RuntimeStatus:   telemt.RuntimeStatusHealthy,
			ComposeStatus:   telemt.ComposeStatusRunning,
			ComposeReason:   "compose_ps_running",
			HealthStatus:    telemt.HealthStatusHealthy,
			HealthReason:    "health_ok",
			LinkStatus:      telemt.LinkStatusAvailable,
			LinkReason:      "link_ok",
			ProxyLink:       link,
		}, nil
	}

	ctx, stdout, logs := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status command to succeed, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Runtime status: healthy") {
		t.Fatalf("expected healthy status output, got: %s", rendered)
	}
	if strings.Contains(rendered, link) {
		t.Fatalf("expected status output to keep proxy link redacted, got: %s", rendered)
	}
	if !strings.Contains(rendered, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction marker in status output, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "status command entry") {
		t.Fatalf("expected command entry log, got: %s", logText)
	}
	if !strings.Contains(logText, "status detected provider") {
		t.Fatalf("expected detected-provider log, got: %s", logText)
	}
	if !strings.Contains(logText, "final runtime summary") {
		t.Fatalf("expected final summary log, got: %s", logText)
	}
	if strings.Contains(logText, link) {
		t.Fatalf("expected logs to keep proxy link redacted, got: %s", logText)
	}
}

func TestRunStatusUnsupportedProviderFallsBackToPartialSummary(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{
			result: execadapter.Result{
				Stdout: "mtg  Up 3 minutes",
			},
		}, nil
	}
	collectTelemtStatus = func(context.Context, telemt.StatusCollectorOptions) (telemt.StatusSummary, error) {
		t.Fatal("collectTelemtStatus must not be called for unsupported provider")
		return telemt.StatusSummary{}, nil
	}

	ctx, stdout, logs := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status command to succeed with partial summary, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Runtime status: partial") {
		t.Fatalf("expected partial runtime summary, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Provider: mtg") {
		t.Fatalf("expected provider marker for mtg, got: %s", rendered)
	}
	if !strings.Contains(rendered, "unsupported_for_provider") {
		t.Fatalf("expected unsupported marker in output, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "status unsupported-provider fallback") {
		t.Fatalf("expected unsupported-provider fallback log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Fatalf("expected WARN logs for unsupported provider, got: %s", logText)
	}
}

func TestRunStatusRendersComposeInitFailureForUnsupportedFallback(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return nil, errors.New("compose binary not found")
	}

	ctx, stdout, logs := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status command to return unsupported fallback summary, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Compose: error (compose_adapter_init_failed)") {
		t.Fatalf("expected compose init failure marker in fallback output, got: %s", rendered)
	}
	if strings.Contains(rendered, "Compose: skipped") {
		t.Fatalf("expected compose init failure to avoid skipped marker, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Compose diagnostics: compose binary not found") {
		t.Fatalf("expected compose init diagnostics in fallback output, got: %s", rendered)
	}

	if !strings.Contains(logs.String(), "status unsupported-provider fallback") {
		t.Fatalf("expected unsupported fallback log, got: %s", logs.String())
	}
}

func TestRunStatusTelemtComposeInitDegradesInsteadOfHardFail(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return nil, errors.New("compose init failed")
	}
	newTelemtAPI = func(*runtime.RuntimeInstallation, *slog.Logger) (telemtStatusAPI, error) {
		return &stubTelemtStatusAPI{
			endpoint: "http://127.0.0.1:9091",
			health:   healthyStatusHealthFetch(),
			users:    usersStatusFetchWithLink("tg://proxy?server=127.0.0.1&port=443&secret=abcdef"),
		}, nil
	}
	collectTelemtStatus = telemt.CollectStatus

	ctx, stdout, logs := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status command to degrade instead of hard fail, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Runtime status: degraded") {
		t.Fatalf("expected degraded summary for compose init failure, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Health: healthy (health_ok)") {
		t.Fatalf("expected health data to remain available, got: %s", rendered)
	}
	if !strings.Contains(rendered, "[redacted-proxy-link]") {
		t.Fatalf("expected redacted link marker in degraded status output, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "telemt compose adapter init degraded") {
		t.Fatalf("expected compose init degradation warning, got: %s", logText)
	}
	if !strings.Contains(logText, "telemt status resolved with adapter-init degradation") {
		t.Fatalf("expected adapter-init degradation summary warning, got: %s", logText)
	}
}

func TestRunStatusTelemtAPIInitDegradesInsteadOfHardFail(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderTelemt), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{
			result: execadapter.Result{
				Stdout: "NAME IMAGE STATUS\ntelemt image Up 5 minutes",
			},
		}, nil
	}
	newTelemtAPI = func(*runtime.RuntimeInstallation, *slog.Logger) (telemtStatusAPI, error) {
		return nil, errors.New("api init failed")
	}
	collectTelemtStatus = telemt.CollectStatus

	ctx, stdout, logs := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status command to degrade instead of hard fail, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Compose: running (compose_ps_running)") {
		t.Fatalf("expected compose data to remain available, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Health: unreachable") {
		t.Fatalf("expected degraded API health state, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Link: unreachable") {
		t.Fatalf("expected degraded link state, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "telemt api bridge init degraded") {
		t.Fatalf("expected api init degradation warning, got: %s", logText)
	}
	if !strings.Contains(logText, "telemt status resolved with adapter-init degradation") {
		t.Fatalf("expected adapter-init degradation summary warning, got: %s", logText)
	}
}

func TestRunStatusReturnsErrorForHardRuntimeFailure(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return nil, errors.New("hard failure tg://proxy?server=127.0.0.1&secret=abcdef")
	}

	ctx, _, logs := newTestCommandContext("status")
	err := runStatus(ctx)
	if err == nil {
		t.Fatalf("expected status command to fail on hard runtime error")
	}

	logText := logs.String()
	if !strings.Contains(logText, "status resolution failed") {
		t.Fatalf("expected status failure log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR log level for hard failure, got: %s", logText)
	}
	if strings.Contains(logText, "tg://proxy?") {
		t.Fatalf("expected hard failure logs to redact proxy link, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted-proxy-link]") {
		t.Fatalf("expected redaction marker in logs, got: %s", logText)
	}
}

func TestRunStatusUsesFallbackInstallEnvForUnsupportedRuntimeErrors(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	tempDir := t.TempDir()
	t.Setenv(runtime.InstallDirEnvKey, tempDir)
	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte("PROVIDER=mtg\n"), 0o600); err != nil {
		t.Fatalf("failed to write fallback env file: %v", err)
	}

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return nil, &runtime.RuntimeError{
			Code:    runtime.CodeProviderAmbiguous,
			Path:    tempDir,
			Message: "provider detection is ambiguous",
		}
	}

	ctx, stdout, logs := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status fallback to succeed, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Provider: mtg") {
		t.Fatalf("expected provider from fallback env, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Compose: skipped") {
		t.Fatalf("expected compose skipped marker for runtime fallback, got: %s", rendered)
	}

	if !strings.Contains(logs.String(), "status unsupported-provider fallback") {
		t.Fatalf("expected unsupported fallback log, got: %s", logs.String())
	}
}

func TestRunStatusReturnsErrorForRuntimeProviderMismatch(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return nil, &runtime.RuntimeError{
			Code:    runtime.CodeProviderMismatch,
			Path:    "/opt/mtproxy-installer/providers/telemt/telemt.toml",
			Message: "runtime provider mismatch: env declares telemt but only mtg config exists",
		}
	}

	ctx, _, logs := newTestCommandContext("status")
	err := runStatus(ctx)
	if err == nil {
		t.Fatalf("expected status command to fail for provider mismatch")
	}
	if !strings.Contains(err.Error(), "runtime provider mismatch") {
		t.Fatalf("expected provider mismatch error, got: %v", err)
	}

	logText := logs.String()
	if !strings.Contains(logText, "runtime provider mismatch detected") {
		t.Fatalf("expected explicit provider mismatch log, got: %s", logText)
	}
	if strings.Contains(logText, "unsupported-provider fallback") {
		t.Fatalf("expected mismatch path to avoid unsupported fallback log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR log for failed mismatch resolution, got: %s", logText)
	}
}

func TestRunStatusFallbackEnvPathHardeningRejectsSymlinkedEnvFile(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	tempDir := t.TempDir()
	targetEnv := filepath.Join(tempDir, "target.env")
	if err := os.WriteFile(targetEnv, []byte("PROVIDER=mtg\n"), 0o600); err != nil {
		t.Fatalf("failed to write target env file: %v", err)
	}
	envPath := filepath.Join(tempDir, ".env")
	if err := os.Symlink(targetEnv, envPath); err != nil {
		t.Skipf("symlink creation is unavailable in this environment: %v", err)
	}

	t.Setenv(runtime.InstallDirEnvKey, tempDir)
	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return nil, &runtime.RuntimeError{
			Code:    runtime.CodeProviderAmbiguous,
			Path:    tempDir,
			Message: "provider detection is ambiguous",
		}
	}

	ctx, stdout, _ := newTestCommandContext("status")
	if err := runStatus(ctx); err != nil {
		t.Fatalf("expected status fallback to succeed with diagnostics, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Provider: unknown") {
		t.Fatalf("expected hardened fallback to avoid reading symlinked env provider, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Runtime diagnostics:") {
		t.Fatalf("expected runtime diagnostics for hardened fallback rejection, got: %s", rendered)
	}
}

type cliDependencySnapshot struct {
	runtimeLoad         func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error)
	newCompose          func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error)
	newTelemtAPI        func(*runtime.RuntimeInstallation, *slog.Logger) (telemtStatusAPI, error)
	collectTelemtStatus func(context.Context, telemt.StatusCollectorOptions) (telemt.StatusSummary, error)
}

func snapshotCLIDependencies() cliDependencySnapshot {
	return cliDependencySnapshot{
		runtimeLoad:         runtimeLoad,
		newCompose:          newCompose,
		newTelemtAPI:        newTelemtAPI,
		collectTelemtStatus: collectTelemtStatus,
	}
}

func restoreCLIDependencies(snapshot cliDependencySnapshot) {
	runtimeLoad = snapshot.runtimeLoad
	newCompose = snapshot.newCompose
	newTelemtAPI = snapshot.newTelemtAPI
	collectTelemtStatus = snapshot.collectTelemtStatus
}

type stubComposeRunner struct {
	result execadapter.Result
	err    error
}

func (s *stubComposeRunner) Run(context.Context, docker.ComposeCommand) (execadapter.Result, error) {
	return s.result, s.err
}

type stubTelemtStatusAPI struct {
	endpoint  string
	health    telemtapi.HealthFetch
	healthErr error
	users     telemtapi.UsersFetch
	usersErr  error
}

func (s *stubTelemtStatusAPI) ReadHealth(context.Context) (telemtapi.HealthFetch, error) {
	if s.healthErr != nil {
		return telemtapi.HealthFetch{}, s.healthErr
	}
	return s.health, nil
}

func (s *stubTelemtStatusAPI) ResolveStartupLink(context.Context) (telemtapi.UsersFetch, error) {
	if s.usersErr != nil {
		return telemtapi.UsersFetch{}, s.usersErr
	}
	return s.users, nil
}

func (s *stubTelemtStatusAPI) ControlEndpoint() string {
	return s.endpoint
}

func runtimeForProvider(provider runtime.Provider) *runtime.RuntimeInstallation {
	return &runtime.RuntimeInstallation{
		Paths: runtime.RuntimePaths{
			InstallDir: "/opt/mtproxy-installer",
		},
		Provider: runtime.ProviderDescriptor{
			Name: provider,
		},
	}
}

func healthyStatusHealthFetch() telemtapi.HealthFetch {
	return telemtapi.HealthFetch{
		ParseClass: telemtapi.HealthParseClassComplete,
		Payload: telemtapi.HealthEnvelope{
			OK: boolPointerStatusTest(true),
			Data: &telemtapi.HealthData{
				Status:   "ok",
				ReadOnly: boolPointerStatusTest(true),
			},
		},
	}
}

func usersStatusFetchWithLink(link string) telemtapi.UsersFetch {
	return telemtapi.UsersFetch{
		Selection: telemtapi.LinkSelection{
			Class:          telemtapi.UsersParseClassUsableLink,
			SelectedLink:   link,
			UsersCount:     1,
			CandidateCount: 1,
		},
	}
}

func boolPointerStatusTest(value bool) *bool {
	return &value
}

func newTestCommandContext(command string, args ...string) (commandContext, *bytes.Buffer, *bytes.Buffer) {
	var stdout bytes.Buffer
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx := commandContext{
		Logger:  logger,
		Stdout:  &stdout,
		Stderr:  &logs,
		Command: command,
		Args:    args,
	}
	return ctx, &stdout, &logs
}
