package telemt

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"mtproxy-installer/app/internal/docker"
	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

func TestCollectStatusResolvesHealthyRuntime(t *testing.T) {
	logger, logs := testLogger()
	link := "tg://proxy?server=127.0.0.1&port=443&secret=abcdef"

	summary, err := CollectStatus(context.Background(), StatusCollectorOptions{
		Runtime: telemtRuntimeForStatus(),
		Compose: &fakeComposeRunner{
			result: execadapter.Result{
				Stdout: "NAME      IMAGE   STATUS\ntelemt    image   Up 2 minutes",
			},
		},
		API: &fakeStatusAPI{
			endpoint: "http://127.0.0.1:9091",
			health:   healthyHealthFetch(),
			users: telemtapi.UsersFetch{
				Selection: telemtapi.LinkSelection{
					Class:          telemtapi.UsersParseClassUsableLink,
					SelectedLink:   link,
					UsersCount:     1,
					CandidateCount: 1,
				},
			},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("expected status collection to succeed, got error: %v", err)
	}

	if summary.RuntimeStatus != RuntimeStatusHealthy {
		t.Fatalf("unexpected runtime status: got %q, want %q", summary.RuntimeStatus, RuntimeStatusHealthy)
	}
	if summary.ComposeStatus != ComposeStatusRunning {
		t.Fatalf("unexpected compose status: got %q, want %q", summary.ComposeStatus, ComposeStatusRunning)
	}
	if summary.HealthStatus != HealthStatusHealthy {
		t.Fatalf("unexpected health status: got %q, want %q", summary.HealthStatus, HealthStatusHealthy)
	}
	if summary.LinkStatus != LinkStatusAvailable {
		t.Fatalf("unexpected link status: got %q, want %q", summary.LinkStatus, LinkStatusAvailable)
	}
	if !summary.LinkAvailable() {
		t.Fatalf("expected link to be available")
	}

	logText := logs.String()
	if !strings.Contains(logText, "telemt status health source selection") {
		t.Fatalf("expected health source selection log, got: %s", logText)
	}
	if !strings.Contains(logText, "telemt status reconciliation") {
		t.Fatalf("expected reconciliation log, got: %s", logText)
	}
	if strings.Contains(logText, link) {
		t.Fatalf("expected logs to keep proxy link redacted, got: %s", logText)
	}
}

func TestCollectStatusMarksDegradedWhenHealthPayloadIsIncomplete(t *testing.T) {
	logger, _ := testLogger()

	summary, err := CollectStatus(context.Background(), StatusCollectorOptions{
		Runtime: telemtRuntimeForStatus(),
		Compose: &fakeComposeRunner{
			result: execadapter.Result{
				Stdout: "telemt up",
			},
		},
		API: &fakeStatusAPI{
			endpoint: "http://127.0.0.1:9091",
			health: telemtapi.HealthFetch{
				ParseClass:     telemtapi.HealthParseClassIncomplete,
				DegradedReason: "missing_read_only",
			},
			users: telemtapi.UsersFetch{
				Selection: telemtapi.LinkSelection{
					Class:          telemtapi.UsersParseClassUsableLink,
					SelectedLink:   "tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
					UsersCount:     1,
					CandidateCount: 1,
				},
			},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("expected status collection to succeed, got error: %v", err)
	}

	if summary.RuntimeStatus != RuntimeStatusDegraded {
		t.Fatalf("unexpected runtime status: got %q, want %q", summary.RuntimeStatus, RuntimeStatusDegraded)
	}
	if !containsReason(summary.DegradedReason, "missing_read_only") {
		t.Fatalf("expected degraded reasons to include missing_read_only, got: %v", summary.DegradedReason)
	}
}

func TestCollectStatusMarksAPIUnreachableWhenUsersFetchFails(t *testing.T) {
	logger, _ := testLogger()

	summary, err := CollectStatus(context.Background(), StatusCollectorOptions{
		Runtime: telemtRuntimeForStatus(),
		Compose: &fakeComposeRunner{
			result: execadapter.Result{
				Stdout: "telemt up",
			},
		},
		API: &fakeStatusAPI{
			endpoint: "http://127.0.0.1:9091",
			health:   healthyHealthFetch(),
			usersErr: &telemtapi.RequestError{
				Kind: telemtapi.RequestErrorKindTransport,
				Path: "/v1/users",
				Err:  errors.New("dial tcp timeout"),
			},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("expected status collection to succeed with degraded data, got error: %v", err)
	}

	if summary.RuntimeStatus != RuntimeStatusAPIUnreachable {
		t.Fatalf("unexpected runtime status: got %q, want %q", summary.RuntimeStatus, RuntimeStatusAPIUnreachable)
	}
	if summary.LinkStatus != LinkStatusUnreachable {
		t.Fatalf("unexpected link status: got %q, want %q", summary.LinkStatus, LinkStatusUnreachable)
	}
	if !containsReason(summary.DegradedReason, "users_transport_error") {
		t.Fatalf("expected degraded reasons to include users_transport_error, got: %v", summary.DegradedReason)
	}
}

func TestCollectStatusComposeNotRunningHasPriority(t *testing.T) {
	logger, _ := testLogger()

	summary, err := CollectStatus(context.Background(), StatusCollectorOptions{
		Runtime: telemtRuntimeForStatus(),
		Compose: &fakeComposeRunner{
			result: execadapter.Result{
				Stdout: "",
			},
		},
		API: &fakeStatusAPI{
			endpoint: "http://127.0.0.1:9091",
			healthErr: &telemtapi.RequestError{
				Kind: telemtapi.RequestErrorKindTransport,
				Path: "/v1/health",
				Err:  errors.New("dial tcp timeout"),
			},
			usersErr: &telemtapi.RequestError{
				Kind: telemtapi.RequestErrorKindTransport,
				Path: "/v1/users",
				Err:  errors.New("dial tcp timeout"),
			},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("expected status collection to succeed with degraded data, got error: %v", err)
	}

	if summary.RuntimeStatus != RuntimeStatusComposeNotRunning {
		t.Fatalf("unexpected runtime status: got %q, want %q", summary.RuntimeStatus, RuntimeStatusComposeNotRunning)
	}
	if !containsReason(summary.DegradedReason, "compose_ps_empty_output") {
		t.Fatalf("expected compose reason in degraded reasons, got: %v", summary.DegradedReason)
	}
}

func TestCollectStatusReturnsErrorForMissingRuntime(t *testing.T) {
	logger, logs := testLogger()

	_, err := CollectStatus(context.Background(), StatusCollectorOptions{
		Compose: &fakeComposeRunner{},
		API: &fakeStatusAPI{
			endpoint: "http://127.0.0.1:9091",
		},
		Logger: logger,
	})
	if err == nil {
		t.Fatalf("expected collector to fail for missing runtime")
	}

	if !strings.Contains(logs.String(), "telemt status resolution failed") {
		t.Fatalf("expected error log for failed status resolution, got: %s", logs.String())
	}
}

type fakeComposeRunner struct {
	result execadapter.Result
	err    error
}

func (f *fakeComposeRunner) Run(context.Context, docker.ComposeCommand) (execadapter.Result, error) {
	return f.result, f.err
}

type fakeStatusAPI struct {
	endpoint  string
	health    telemtapi.HealthFetch
	healthErr error
	users     telemtapi.UsersFetch
	usersErr  error
}

func (f *fakeStatusAPI) ReadHealth(context.Context) (telemtapi.HealthFetch, error) {
	if f.healthErr != nil {
		return telemtapi.HealthFetch{}, f.healthErr
	}
	return f.health, nil
}

func (f *fakeStatusAPI) ResolveStartupLink(context.Context) (telemtapi.UsersFetch, error) {
	if f.usersErr != nil {
		return telemtapi.UsersFetch{}, f.usersErr
	}
	return f.users, nil
}

func (f *fakeStatusAPI) ControlEndpoint() string {
	return f.endpoint
}

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buffer
}

func telemtRuntimeForStatus() *runtime.RuntimeInstallation {
	return &runtime.RuntimeInstallation{
		Paths: runtime.RuntimePaths{
			InstallDir: "/opt/mtproxy-installer",
		},
		Provider: runtime.ProviderDescriptor{
			Name: runtime.ProviderTelemt,
		},
	}
}

func healthyHealthFetch() telemtapi.HealthFetch {
	return telemtapi.HealthFetch{
		ParseClass: telemtapi.HealthParseClassComplete,
		Payload: telemtapi.HealthEnvelope{
			OK: boolPointer(true),
			Data: &telemtapi.HealthData{
				Status:   "ok",
				ReadOnly: boolPointer(true),
			},
		},
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func containsReason(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
