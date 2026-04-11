package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"log/slog"

	"mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
)

func TestRunLinkPrintsFullLinkOnlyToStdout(t *testing.T) {
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
			Provider:      runtime.ProviderTelemt,
			RuntimeStatus: telemt.RuntimeStatusHealthy,
			ComposeStatus: telemt.ComposeStatusRunning,
			HealthStatus:  telemt.HealthStatusHealthy,
			LinkStatus:    telemt.LinkStatusAvailable,
			LinkReason:    "link_ok",
			ProxyLink:     link,
		}, nil
	}

	ctx, stdout, logs := newTestCommandContext("link")
	if err := runLink(ctx); err != nil {
		t.Fatalf("expected link command to succeed, got error: %v", err)
	}

	rendered := strings.TrimSpace(stdout.String())
	if rendered != link {
		t.Fatalf("expected full proxy link in stdout, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "link command resolved") {
		t.Fatalf("expected link resolution log, got: %s", logText)
	}
	if strings.Contains(logText, link) {
		t.Fatalf("expected logs to keep proxy link redacted, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted-proxy-link]") {
		t.Fatalf("expected redacted marker in logs, got: %s", logText)
	}
}

func TestRunLinkWarnsWhenLinkIsUnavailable(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

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
			Provider:      runtime.ProviderTelemt,
			RuntimeStatus: telemt.RuntimeStatusLinkUnavailable,
			ComposeStatus: telemt.ComposeStatusRunning,
			HealthStatus:  telemt.HealthStatusHealthy,
			LinkStatus:    telemt.LinkStatusUnavailable,
			LinkReason:    "users_without_tls_links",
		}, nil
	}

	ctx, stdout, logs := newTestCommandContext("link")
	if err := runLink(ctx); err != nil {
		t.Fatalf("expected link command to return actionable fallback, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Proxy link unavailable for telemt runtime.") {
		t.Fatalf("expected unavailable message in stdout, got: %s", rendered)
	}
	if strings.Contains(rendered, "tg://proxy?") {
		t.Fatalf("expected unavailable output to avoid full proxy link, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "link command resolved without usable link") {
		t.Fatalf("expected degraded link log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=WARN") {
		t.Fatalf("expected WARN level for unavailable link path, got: %s", logText)
	}
}

func TestRunLinkUnsupportedProviderFallback(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{
			result: exec.Result{
				Stdout: "mtg  Up 2 minutes",
			},
		}, nil
	}

	ctx, stdout, logs := newTestCommandContext("link")
	if err := runLink(ctx); err != nil {
		t.Fatalf("expected link command to handle unsupported provider, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Proxy link is unavailable") {
		t.Fatalf("expected unsupported provider message, got: %s", rendered)
	}
	if !strings.Contains(rendered, `provider "mtg"`) {
		t.Fatalf("expected provider marker in unsupported output, got: %s", rendered)
	}

	if !strings.Contains(logs.String(), "link unsupported-provider fallback") {
		t.Fatalf("expected unsupported-provider fallback log, got: %s", logs.String())
	}
}

func TestRunLinkUnsupportedProviderComposeInitFailure(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return nil, errors.New("compose adapter init failed: docker socket denied")
	}

	ctx, stdout, _ := newTestCommandContext("link")
	if err := runLink(ctx); err != nil {
		t.Fatalf("expected link command to succeed with unsupported fallback, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Compose: error (compose_adapter_init_failed)") {
		t.Fatalf("expected compose init failure in unsupported fallback, got: %s", rendered)
	}
	if strings.Contains(rendered, "Compose: skipped") {
		t.Fatalf("expected compose init failure to avoid skipped marker, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Compose diagnostics: compose adapter init failed: docker socket denied") {
		t.Fatalf("expected compose diagnostics in unsupported fallback output, got: %s", rendered)
	}
}

func TestRunLinkUnsupportedProviderComposeRunErrorKeepsErrorAndStderrDiagnostics(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return runtimeForProvider(runtime.ProviderMTG), nil
	}
	newCompose = func(*runtime.RuntimeInstallation, *slog.Logger) (composeRunner, error) {
		return &stubComposeRunner{
			result: exec.Result{
				StderrSummary: "stderr API_KEY=super-secret",
			},
			err: errors.New("compose run failed: bearer top-secret-token"),
		}, nil
	}

	ctx, stdout, _ := newTestCommandContext("link")
	if err := runLink(ctx); err != nil {
		t.Fatalf("expected link command to succeed with unsupported fallback, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Compose diagnostics: compose run failed") {
		t.Fatalf("expected compose run error diagnostics in unsupported fallback output, got: %s", rendered)
	}
	if !strings.Contains(rendered, "stderr API_KEY=[redacted]") {
		t.Fatalf("expected compose stderr diagnostics in unsupported fallback output, got: %s", rendered)
	}
	if strings.Contains(rendered, "top-secret-token") || strings.Contains(rendered, "super-secret") {
		t.Fatalf("expected unsupported fallback diagnostics to be redacted, got: %s", rendered)
	}
}

func TestRunLinkTelemtComposeInitDegradesInsteadOfHardFail(t *testing.T) {
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

	ctx, stdout, logs := newTestCommandContext("link")
	if err := runLink(ctx); err != nil {
		t.Fatalf("expected link command to degrade instead of hard fail, got error: %v", err)
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Proxy link unavailable for telemt runtime.") {
		t.Fatalf("expected degraded link output when compose init fails, got: %s", rendered)
	}
	if strings.Contains(rendered, "tg://proxy?") {
		t.Fatalf("expected degraded link output to avoid raw proxy link, got: %s", rendered)
	}

	logText := logs.String()
	if !strings.Contains(logText, "telemt compose adapter init degraded") {
		t.Fatalf("expected compose init degradation warning, got: %s", logText)
	}
	if !strings.Contains(logText, "link command resolved without usable link") {
		t.Fatalf("expected degraded link resolution warning, got: %s", logText)
	}
}

func TestRunLinkReturnsErrorForHardRuntimeFailure(t *testing.T) {
	deps := snapshotCLIDependencies()
	t.Cleanup(func() {
		restoreCLIDependencies(deps)
	})

	runtimeLoad = func(runtime.LoadOptions) (*runtime.RuntimeInstallation, error) {
		return nil, errors.New("hard runtime failure")
	}

	ctx, _, logs := newTestCommandContext("link")
	err := runLink(ctx)
	if err == nil {
		t.Fatalf("expected link command to fail on hard runtime error")
	}

	logText := logs.String()
	if !strings.Contains(logText, "link resolution failed") {
		t.Fatalf("expected link resolution failure log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR level for hard runtime failure, got: %s", logText)
	}
}

func TestRunLinkReturnsErrorForRuntimeProviderMismatch(t *testing.T) {
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

	ctx, _, logs := newTestCommandContext("link")
	err := runLink(ctx)
	if err == nil {
		t.Fatalf("expected link command to fail for provider mismatch")
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
}
