package telemt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"mtproxy-installer/app/internal/docker"
	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

type RuntimeStatus string

const (
	RuntimeStatusHealthy           RuntimeStatus = "healthy"
	RuntimeStatusDegraded          RuntimeStatus = "degraded"
	RuntimeStatusAPIUnreachable    RuntimeStatus = "api_unreachable"
	RuntimeStatusLinkUnavailable   RuntimeStatus = "link_unavailable"
	RuntimeStatusComposeNotRunning RuntimeStatus = "compose_not_running"
)

type ComposeStatus string

const (
	ComposeStatusRunning    ComposeStatus = "running"
	ComposeStatusNotRunning ComposeStatus = "not_running"
	ComposeStatusUnknown    ComposeStatus = "unknown"
)

type HealthStatus string

const (
	HealthStatusHealthy     HealthStatus = "healthy"
	HealthStatusDegraded    HealthStatus = "degraded"
	HealthStatusUnreachable HealthStatus = "unreachable"
)

type LinkStatus string

const (
	LinkStatusAvailable   LinkStatus = "available"
	LinkStatusUnavailable LinkStatus = "unavailable"
	LinkStatusUnreachable LinkStatus = "unreachable"
)

type StatusSummary struct {
	Provider        runtime.Provider
	InstallDir      string
	ControlEndpoint string

	ComposeStatus ComposeStatus
	ComposeReason string

	HealthStatus HealthStatus
	HealthClass  telemtapi.HealthParseClass
	HealthReason string

	LinkStatus LinkStatus
	LinkClass  telemtapi.UsersParseClass
	LinkReason string
	ProxyLink  string

	UsersCount     int
	TLSCandidates  int
	RuntimeStatus  RuntimeStatus
	DegradedReason []string
}

func (s StatusSummary) LinkAvailable() bool {
	return strings.TrimSpace(s.ProxyLink) != ""
}

func (s StatusSummary) RedactedProxyLink() string {
	if !s.LinkAvailable() {
		return ""
	}
	return "[redacted-proxy-link]"
}

type StatusCollectorOptions struct {
	Runtime *runtime.RuntimeInstallation
	Compose composeRunner
	API     statusAPI
	Logger  *slog.Logger
}

type composeRunner interface {
	Run(context.Context, docker.ComposeCommand) (execadapter.Result, error)
}

type statusAPI interface {
	ReadHealth(context.Context) (telemtapi.HealthFetch, error)
	ResolveStartupLink(context.Context) (telemtapi.UsersFetch, error)
	ControlEndpoint() string
}

func CollectStatus(ctx context.Context, options StatusCollectorOptions) (StatusSummary, error) {
	logger := fallbackLogger(options.Logger)
	if ctx == nil {
		ctx = context.Background()
	}

	if options.Runtime == nil {
		err := errors.New("telemt status collector requires runtime installation")
		logger.Error("telemt status resolution failed", "error", err.Error())
		return StatusSummary{}, err
	}
	if options.Runtime.Provider.Name != runtime.ProviderTelemt {
		err := fmt.Errorf(
			"telemt status collector requires provider telemt, got %q",
			options.Runtime.Provider.Name,
		)
		logger.Error("telemt status resolution failed", "error", err.Error())
		return StatusSummary{}, err
	}
	if options.Compose == nil {
		err := errors.New("telemt status collector requires compose runner")
		logger.Error("telemt status resolution failed", "error", err.Error())
		return StatusSummary{}, err
	}
	if options.API == nil {
		err := errors.New("telemt status collector requires telemt api bridge")
		logger.Error("telemt status resolution failed", "error", err.Error())
		return StatusSummary{}, err
	}

	summary := StatusSummary{
		Provider:        options.Runtime.Provider.Name,
		InstallDir:      options.Runtime.Paths.InstallDir,
		ControlEndpoint: options.API.ControlEndpoint(),
		ComposeStatus:   ComposeStatusUnknown,
		ComposeReason:   "compose_ps_not_attempted",
		HealthStatus:    HealthStatusUnreachable,
		HealthReason:    "health_not_attempted",
		LinkStatus:      LinkStatusUnreachable,
		LinkReason:      "link_not_attempted",
	}

	logger.Info(
		"telemt status collection start",
		"provider", summary.Provider,
		"install_dir", summary.InstallDir,
		"control_endpoint", summary.ControlEndpoint,
	)
	logger.Info(
		"telemt status health source selection",
		"compose_source", "docker_compose_ps",
		"health_source", "/v1/health",
		"users_source", "/v1/users",
	)

	composeResult, composeErr := options.Compose.Run(ctx, docker.ComposeCommand{
		Subcommand: "ps",
	})
	if composeErr != nil {
		summary.ComposeStatus = ComposeStatusUnknown
		summary.ComposeReason = "compose_ps_failed"
		logger.Warn(
			"telemt status compose ps degraded",
			"compose_state", summary.ComposeStatus,
			"compose_reason", summary.ComposeReason,
			"stderr_summary", composeResult.StderrSummary,
			"error", composeErr.Error(),
		)
	} else {
		summary.ComposeStatus, summary.ComposeReason = ClassifyComposePSOutput(composeResult.Stdout)
		logger.Info(
			"telemt status compose ps classified",
			"compose_state", summary.ComposeStatus,
			"compose_reason", summary.ComposeReason,
		)
	}

	healthFetch, healthErr := options.API.ReadHealth(ctx)
	if healthErr != nil {
		summary.HealthStatus = HealthStatusUnreachable
		summary.HealthReason = classifyRequestError("health", healthErr)
		logger.Warn(
			"telemt status health fetch degraded",
			"health_state", summary.HealthStatus,
			"health_reason", summary.HealthReason,
		)
	} else {
		summary.HealthClass = healthFetch.ParseClass
		summary.HealthStatus, summary.HealthReason = classifyHealth(healthFetch)
		logger.Info(
			"telemt status health classified",
			"health_state", summary.HealthStatus,
			"health_class", summary.HealthClass,
			"health_reason", summary.HealthReason,
		)
	}

	usersFetch, usersErr := options.API.ResolveStartupLink(ctx)
	if usersErr != nil {
		summary.LinkStatus = LinkStatusUnreachable
		summary.LinkReason = classifyRequestError("users", usersErr)
		logger.Warn(
			"telemt status users fetch degraded",
			"link_state", summary.LinkStatus,
			"link_reason", summary.LinkReason,
		)
	} else {
		summary.LinkClass = usersFetch.Selection.Class
		summary.UsersCount = usersFetch.Selection.UsersCount
		summary.TLSCandidates = usersFetch.Selection.CandidateCount
		summary.LinkStatus, summary.LinkReason, summary.ProxyLink = classifyLink(usersFetch)
		logger.Info(
			"telemt status users classified",
			"link_state", summary.LinkStatus,
			"link_class", summary.LinkClass,
			"link_reason", summary.LinkReason,
			"users_count", summary.UsersCount,
			"tls_candidates", summary.TLSCandidates,
			"link_available", summary.LinkAvailable(),
		)
	}

	summary.RuntimeStatus, summary.DegradedReason = reconcileRuntime(summary)
	logger.Info(
		"telemt status reconciliation",
		"compose_state", summary.ComposeStatus,
		"health_state", summary.HealthStatus,
		"link_state", summary.LinkStatus,
		"runtime_status", summary.RuntimeStatus,
		"degraded_reasons", summary.DegradedReason,
	)

	if summary.RuntimeStatus == RuntimeStatusHealthy {
		logger.Info(
			"telemt status collection resolved",
			"provider", summary.Provider,
			"runtime_status", summary.RuntimeStatus,
			"compose_state", summary.ComposeStatus,
			"health_state", summary.HealthStatus,
			"link_state", summary.LinkStatus,
			"link_available", summary.LinkAvailable(),
		)
	} else {
		logger.Warn(
			"telemt status collection resolved with degradation",
			"provider", summary.Provider,
			"runtime_status", summary.RuntimeStatus,
			"compose_state", summary.ComposeStatus,
			"health_state", summary.HealthStatus,
			"link_state", summary.LinkStatus,
			"link_available", summary.LinkAvailable(),
			"degraded_reasons", summary.DegradedReason,
		)
	}

	return summary, nil
}

func ClassifyComposePSOutput(stdout string) (ComposeStatus, string) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return ComposeStatusNotRunning, "compose_ps_empty_output"
	}

	lines := strings.Split(trimmed, "\n")
	filtered := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "name ") {
			continue
		}
		filtered = append(filtered, lower)
	}

	if len(filtered) == 0 {
		return ComposeStatusNotRunning, "compose_ps_no_service_rows"
	}

	hasRunning := false
	hasStopped := false
	for _, line := range filtered {
		if strings.Contains(line, " up ") || strings.HasSuffix(line, " up") || strings.Contains(line, " running") {
			hasRunning = true
		}
		if strings.Contains(line, " exited") || strings.Contains(line, " dead") || strings.Contains(line, " created") || strings.Contains(line, " restarting") || strings.Contains(line, " stopped") {
			hasStopped = true
		}
	}

	if hasRunning {
		if hasStopped {
			return ComposeStatusRunning, "compose_ps_mixed_container_states"
		}
		return ComposeStatusRunning, "compose_ps_running"
	}
	if hasStopped {
		return ComposeStatusNotRunning, "compose_ps_not_running"
	}

	return ComposeStatusUnknown, "compose_ps_unclassified"
}

func classifyHealth(result telemtapi.HealthFetch) (HealthStatus, string) {
	if result.ParseClass != telemtapi.HealthParseClassComplete {
		reason := strings.TrimSpace(result.DegradedReason)
		if reason == "" {
			reason = "health_incomplete_payload"
		}
		return HealthStatusDegraded, reason
	}
	if result.Payload.IsHealthy() {
		return HealthStatusHealthy, "health_ok"
	}
	if result.Payload.OK != nil && !*result.Payload.OK {
		return HealthStatusDegraded, "health_not_ok"
	}
	if result.Payload.Data != nil {
		status := normalizeReason(result.Payload.Data.Status)
		if status != "" {
			return HealthStatusDegraded, "health_status_" + status
		}
	}
	return HealthStatusDegraded, "health_not_ok"
}

func classifyLink(result telemtapi.UsersFetch) (LinkStatus, string, string) {
	if result.Selection.HasUsableLink() {
		return LinkStatusAvailable, "link_ok", result.Selection.SelectedLink
	}

	reason := strings.TrimSpace(result.Selection.DegradedReason)
	if reason == "" {
		reason = string(result.Selection.Class)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "link_unavailable"
	}

	return LinkStatusUnavailable, reason, ""
}

func classifyRequestError(scope string, err error) string {
	var requestErr *telemtapi.RequestError
	if errors.As(err, &requestErr) {
		return fmt.Sprintf("%s_%s", normalizeReason(scope), normalizeReason(string(requestErr.Kind)))
	}
	return fmt.Sprintf("%s_request_failed", normalizeReason(scope))
}

func reconcileRuntime(summary StatusSummary) (RuntimeStatus, []string) {
	reasons := make([]string, 0, 4)
	appendReason := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return
		}
		for _, existing := range reasons {
			if existing == reason {
				return
			}
		}
		reasons = append(reasons, reason)
	}

	if summary.ComposeStatus == ComposeStatusNotRunning {
		appendReason(summary.ComposeReason)
	}
	if summary.ComposeStatus == ComposeStatusUnknown {
		appendReason(summary.ComposeReason)
	}
	if summary.HealthStatus != HealthStatusHealthy {
		appendReason(summary.HealthReason)
	}
	if summary.LinkStatus != LinkStatusAvailable {
		appendReason(summary.LinkReason)
	}

	switch {
	case summary.ComposeStatus == ComposeStatusNotRunning:
		return RuntimeStatusComposeNotRunning, reasons
	case summary.HealthStatus == HealthStatusUnreachable || summary.LinkStatus == LinkStatusUnreachable:
		return RuntimeStatusAPIUnreachable, reasons
	case summary.HealthStatus == HealthStatusDegraded || summary.ComposeStatus == ComposeStatusUnknown:
		return RuntimeStatusDegraded, reasons
	case summary.LinkStatus == LinkStatusUnavailable:
		return RuntimeStatusLinkUnavailable, reasons
	default:
		return RuntimeStatusHealthy, reasons
	}
}

func normalizeReason(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "_", "-", "_", ".", "_", "/", "_")
	return replacer.Replace(normalized)
}
