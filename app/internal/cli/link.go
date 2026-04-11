package cli

import (
	"mtproxy-installer/app/internal/output"
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

	if telemtSummary.LinkAvailable() {
		if err := writeCommandOutput(ctx.Stdout, output.RenderTelemtLink(telemtSummary)); err != nil {
			return err
		}
		ctx.Logger.Info(
			"link command resolved",
			"provider", telemtSummary.Provider,
			"runtime_status", telemtSummary.RuntimeStatus,
			"compose_state", telemtSummary.ComposeStatus,
			"health_state", telemtSummary.HealthStatus,
			"link_available", true,
			"selected_link", telemtSummary.RedactedProxyLink(),
		)
		logTelemtFinalSummary(ctx.Logger, "link", telemtSummary)
		return nil
	}

	if err := writeCommandOutput(ctx.Stdout, output.RenderTelemtLink(telemtSummary)); err != nil {
		return err
	}
	ctx.Logger.Warn(
		"link command resolved without usable link",
		"provider", telemtSummary.Provider,
		"runtime_status", telemtSummary.RuntimeStatus,
		"compose_state", telemtSummary.ComposeStatus,
		"health_state", telemtSummary.HealthStatus,
		"link_state", telemtSummary.LinkStatus,
		"link_reason", telemtSummary.LinkReason,
		"link_available", false,
	)
	logTelemtFinalSummary(ctx.Logger, "link", telemtSummary)
	return nil
}
