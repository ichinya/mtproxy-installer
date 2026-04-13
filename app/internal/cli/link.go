package cli

import (
	"mtproxy-installer/app/internal/output"
	"mtproxy-installer/app/internal/provider/telemt"
)

func runLink(ctx commandContext) error {
	if err := requireNoArgs("link", ctx.Args); err != nil {
		return err
	}

	ctx.Logger.Info("link command entry", "args_count", len(ctx.Args))

	runtimeState, unsupportedSummary, err := loadRuntimeOrFallback(ctx, "link")
	if err != nil {
		ctx.Logger.Error("link resolution failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	if unsupportedSummary != nil {
		ctx.Logger.Warn(
			"link unsupported-provider fallback",
			"provider", unsupportedSummary.Provider,
			"compose_checked", unsupportedSummary.ComposeChecked,
			"compose_state", unsupportedSummary.ComposeStatus,
			"compose_reason", unsupportedSummary.ComposeReason,
		)
		if err := writeCommandOutput(ctx.Stdout, output.RenderUnsupportedLink(*unsupportedSummary)); err != nil {
			return err
		}
		logUnsupportedFinalSummary(ctx.Logger, "link", *unsupportedSummary)
		return nil
	}

	ctx.Logger.Info(
		"link detected provider",
		"provider", runtimeState.Provider.Name,
		"install_dir", runtimeState.Paths.InstallDir,
	)

	telemtSummary, err := collectTelemtRuntimeStatus(ctx, runtimeState)
	if err != nil {
		ctx.Logger.Error("link resolution failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	linkSummary := sanitizeTelemtLinkOutput(telemtSummary)
	if linkSummary.LinkAvailable() {
		if err := writeCommandOutput(ctx.Stdout, output.RenderTelemtLink(linkSummary)); err != nil {
			return err
		}
		ctx.Logger.Info(
			"link command resolved",
			"provider", linkSummary.Provider,
			"runtime_status", linkSummary.RuntimeStatus,
			"compose_state", linkSummary.ComposeStatus,
			"health_state", linkSummary.HealthStatus,
			"link_available", true,
			"selected_link", linkSummary.RedactedProxyLink(),
		)
		logTelemtFinalSummary(ctx.Logger, "link", linkSummary)
		return nil
	}

	if err := writeCommandOutput(ctx.Stdout, output.RenderTelemtLink(linkSummary)); err != nil {
		return err
	}
	ctx.Logger.Warn(
		"link command resolved without usable link",
		"provider", linkSummary.Provider,
		"runtime_status", linkSummary.RuntimeStatus,
		"compose_state", linkSummary.ComposeStatus,
		"health_state", linkSummary.HealthStatus,
		"link_state", linkSummary.LinkStatus,
		"link_reason", linkSummary.LinkReason,
		"link_available", false,
	)
	logTelemtFinalSummary(ctx.Logger, "link", linkSummary)
	return nil
}

func sanitizeTelemtLinkOutput(summary telemt.StatusSummary) telemt.StatusSummary {
	if summary.ComposeStatus == telemt.ComposeStatusRunning || !summary.LinkAvailable() {
		return summary
	}

	sanitized := summary
	sanitized.ProxyLink = ""
	if sanitized.LinkStatus == telemt.LinkStatusAvailable {
		sanitized.LinkStatus = telemt.LinkStatusUnavailable
	}

	switch sanitized.ComposeStatus {
	case telemt.ComposeStatusNotRunning:
		sanitized.LinkReason = "compose_not_running_link_withheld"
	default:
		sanitized.LinkReason = "compose_unverified_link_withheld"
	}
	return sanitized
}
