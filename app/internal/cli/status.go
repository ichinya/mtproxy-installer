package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"mtproxy-installer/app/internal/docker"
	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/output"
	"mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

type composeRunner interface {
	Run(context.Context, docker.ComposeCommand) (execadapter.Result, error)
}

type telemtStatusAPI interface {
	ReadHealth(context.Context) (telemtapi.HealthFetch, error)
	ResolveStartupLink(context.Context) (telemtapi.UsersFetch, error)
	ControlEndpoint() string
}

var (
	runtimeLoad = runtime.Load
	newCompose  = func(runtimeState *runtime.RuntimeInstallation, logger *slog.Logger) (composeRunner, error) {
		return docker.NewComposeAdapter(docker.ComposeAdapterOptions{
			Runtime: runtimeState,
			Logger:  logger,
		})
	}
	newTelemtAPI = func(runtimeState *runtime.RuntimeInstallation, logger *slog.Logger) (telemtStatusAPI, error) {
		return telemt.NewAPI(telemt.APIOptions{
			Runtime: runtimeState,
			Logger:  logger,
		})
	}
	collectTelemtStatus = func(ctx context.Context, options telemt.StatusCollectorOptions) (telemt.StatusSummary, error) {
		return telemt.CollectStatus(ctx, options)
	}
)

type composeInitFailureRunner struct {
	initErr error
}

func (r *composeInitFailureRunner) Run(context.Context, docker.ComposeCommand) (execadapter.Result, error) {
	diagnostics := ""
	if r != nil && r.initErr != nil {
		diagnostics = execadapter.RedactText(r.initErr.Error())
	}
	return execadapter.Result{
		StderrSummary: diagnostics,
	}, errors.New("compose adapter init failed")
}

type telemtInitFailureBridge struct {
	endpoint string
}

func (b *telemtInitFailureBridge) ReadHealth(context.Context) (telemtapi.HealthFetch, error) {
	return telemtapi.HealthFetch{}, &telemtapi.RequestError{
		Kind: telemtapi.RequestErrorKindTransport,
		Path: "/v1/health",
		Err:  errors.New("telemt api bridge init failed"),
	}
}

func (b *telemtInitFailureBridge) ResolveStartupLink(context.Context) (telemtapi.UsersFetch, error) {
	return telemtapi.UsersFetch{}, &telemtapi.RequestError{
		Kind: telemtapi.RequestErrorKindTransport,
		Path: "/v1/users",
		Err:  errors.New("telemt api bridge init failed"),
	}
}

func (b *telemtInitFailureBridge) ControlEndpoint() string {
	if b == nil {
		return ""
	}
	return strings.TrimSpace(b.endpoint)
}

func runStatus(ctx commandContext) error {
	if err := requireNoArgs("status", ctx.Args); err != nil {
		return err
	}

	ctx.Logger.Info("status command entry", "args_count", len(ctx.Args))

	runtimeState, unsupportedSummary, err := loadRuntimeOrFallback(ctx, "status")
	if err != nil {
		ctx.Logger.Error("status resolution failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	if unsupportedSummary != nil {
		ctx.Logger.Warn(
			"status unsupported-provider fallback",
			"provider", unsupportedSummary.Provider,
			"compose_checked", unsupportedSummary.ComposeChecked,
			"compose_state", unsupportedSummary.ComposeStatus,
			"compose_reason", unsupportedSummary.ComposeReason,
		)
		if err := writeCommandOutput(ctx.Stdout, output.RenderUnsupportedStatus(*unsupportedSummary)); err != nil {
			return err
		}
		logUnsupportedFinalSummary(ctx.Logger, "status", *unsupportedSummary)
		return nil
	}

	ctx.Logger.Info(
		"status detected provider",
		"provider", runtimeState.Provider.Name,
		"install_dir", runtimeState.Paths.InstallDir,
	)

	telemtSummary, err := collectTelemtRuntimeStatus(ctx, runtimeState)
	if err != nil {
		ctx.Logger.Error("status resolution failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	if err := writeCommandOutput(ctx.Stdout, output.RenderTelemtStatus(telemtSummary)); err != nil {
		return err
	}
	logTelemtFinalSummary(ctx.Logger, "status", telemtSummary)
	return nil
}

func collectTelemtRuntimeStatus(
	ctx commandContext,
	runtimeState *runtime.RuntimeInstallation,
) (telemt.StatusSummary, error) {
	composeRunner, composeInitErr := newCompose(runtimeState, ctx.Logger)
	if composeInitErr != nil {
		ctx.Logger.Warn(
			"telemt compose adapter init degraded",
			"provider", runtimeState.Provider.Name,
			"install_dir", runtimeState.Paths.InstallDir,
			"error", redactForCommand(ctx.Command, composeInitErr.Error()),
		)
		composeRunner = &composeInitFailureRunner{
			initErr: composeInitErr,
		}
	}

	apiBridge, apiInitErr := newTelemtAPI(runtimeState, ctx.Logger)
	if apiInitErr != nil {
		fallbackEndpoint := fallbackTelemtControlEndpoint(runtimeState)
		ctx.Logger.Warn(
			"telemt api bridge init degraded",
			"provider", runtimeState.Provider.Name,
			"install_dir", runtimeState.Paths.InstallDir,
			"control_endpoint", fallbackEndpoint,
			"error", redactForCommand(ctx.Command, apiInitErr.Error()),
		)
		apiBridge = &telemtInitFailureBridge{
			endpoint: fallbackEndpoint,
		}
	}

	statusSummary, err := collectTelemtStatus(context.Background(), telemt.StatusCollectorOptions{
		Runtime: runtimeState,
		Compose: composeRunner,
		API:     apiBridge,
		Logger:  ctx.Logger,
	})
	if err != nil {
		return telemt.StatusSummary{}, fmt.Errorf("unable to resolve telemt status: %w", err)
	}

	if composeInitErr != nil || apiInitErr != nil {
		ctx.Logger.Warn(
			"telemt status resolved with adapter-init degradation",
			"provider", statusSummary.Provider,
			"compose_init_failed", composeInitErr != nil,
			"api_init_failed", apiInitErr != nil,
			"compose_state", statusSummary.ComposeStatus,
			"health_state", statusSummary.HealthStatus,
			"link_state", statusSummary.LinkStatus,
			"runtime_status", statusSummary.RuntimeStatus,
			"link_available", statusSummary.LinkAvailable(),
		)
	}

	return statusSummary, nil
}

func loadRuntimeOrFallback(
	ctx commandContext,
	commandName string,
) (*runtime.RuntimeInstallation, *output.UnsupportedProviderSummary, error) {
	ctx.Logger.Debug("runtime load start", "command", commandName)
	runtimeState, err := runtimeLoad(runtime.LoadOptions{
		Logger: ctx.Logger,
	})
	if err == nil {
		ctx.Logger.Debug("runtime load finish", "command", commandName, "success", true)
		if runtimeState.Provider.Name == runtime.ProviderTelemt {
			return runtimeState, nil, nil
		}

		ctx.Logger.Debug(
			"runtime load finish",
			"command", commandName,
			"success", true,
			"unsupported_provider", true,
			"provider", runtimeState.Provider.Name,
		)
		summary := buildUnsupportedRuntimeSummary(ctx, runtimeState, nil)
		return nil, &summary, nil
	}

	if isProviderMismatchError(err) {
		ctx.Logger.Warn(
			"runtime provider mismatch detected",
			"command", commandName,
			"error", redactForCommand(commandName, err.Error()),
		)
		ctx.Logger.Debug(
			"runtime load finish",
			"command", commandName,
			"success", false,
			"unsupported_provider", false,
			"provider_mismatch", true,
			"error", redactForCommand(commandName, err.Error()),
		)
		return nil, nil, fmt.Errorf("runtime provider mismatch: %w", err)
	}

	if !isUnsupportedProviderError(err) {
		ctx.Logger.Debug(
			"runtime load finish",
			"command", commandName,
			"success", false,
			"unsupported_provider", false,
			"error", redactForCommand(commandName, err.Error()),
		)
		return nil, nil, err
	}

	ctx.Logger.Debug(
		"runtime load finish",
		"command", commandName,
		"success", false,
		"unsupported_provider", true,
		"error", redactForCommand(commandName, err.Error()),
	)
	summary := buildUnsupportedRuntimeSummary(ctx, nil, err)
	return nil, &summary, nil
}

func buildUnsupportedRuntimeSummary(
	ctx commandContext,
	runtimeState *runtime.RuntimeInstallation,
	loadErr error,
) output.UnsupportedProviderSummary {
	summary := output.UnsupportedProviderSummary{
		ComposeStatus: telemt.ComposeStatusUnknown,
		ComposeReason: "compose_ps_skipped",
	}
	if loadErr != nil {
		summary.RuntimeResolveError = redactForCommand(ctx.Command, loadErr.Error())
	}

	if runtimeState != nil {
		summary.InstallDir = runtimeState.Paths.InstallDir
		summary.Provider = string(runtimeState.Provider.Name)

		composeRunner, composeErr := newCompose(runtimeState, ctx.Logger)
		if composeErr != nil {
			summary.ComposeChecked = false
			summary.ComposeStatus = telemt.ComposeStatusUnknown
			summary.ComposeReason = "compose_adapter_init_failed"
			summary.ComposeInitError = redactForCommand(ctx.Command, composeErr.Error())
			if summary.RuntimeResolveError == "" {
				summary.RuntimeResolveError = summary.ComposeInitError
			}
			return summary
		}

		composeResult, composeRunErr := composeRunner.Run(context.Background(), docker.ComposeCommand{
			Subcommand: "ps",
		})
		summary.ComposeChecked = true
		if composeRunErr != nil {
			summary.ComposeStatus = telemt.ComposeStatusUnknown
			summary.ComposeReason = "compose_ps_failed"
			summary.ComposeStderr = composeDiagnostics(ctx.Command, composeRunErr, composeResult.StderrSummary)
			return summary
		}

		summary.ComposeStatus, summary.ComposeReason = telemt.ClassifyComposePSOutput(composeResult.Stdout)
		return summary
	}

	installDir := resolveFallbackInstallDir()
	summary.InstallDir = installDir
	summary.Provider = "unknown"

	resolvedInstallDir, envPath, envExists, envPathErr := resolveHardenedFallbackEnvPath(installDir)
	if resolvedInstallDir != "" {
		summary.InstallDir = resolvedInstallDir
	}
	if envPathErr != nil {
		if summary.RuntimeResolveError == "" {
			summary.RuntimeResolveError = redactForCommand(ctx.Command, envPathErr.Error())
		}
		return summary
	}
	if !envExists {
		return summary
	}

	envFile, envErr := runtime.LoadEnv(envPath, ctx.Logger)
	if envErr == nil {
		detectedProvider := strings.TrimSpace(envFile.ProviderValue())
		if detectedProvider != "" {
			summary.Provider = detectedProvider
		}
	} else if summary.RuntimeResolveError == "" {
		summary.RuntimeResolveError = redactForCommand(ctx.Command, envErr.Error())
	}

	return summary
}

func isUnsupportedProviderError(err error) bool {
	var runtimeErr *runtime.RuntimeError
	if !errors.As(err, &runtimeErr) {
		return false
	}

	switch runtimeErr.Code {
	case runtime.CodeProviderUnsupported,
		runtime.CodeProviderAmbiguous,
		runtime.CodeProviderUndetected:
		return true
	default:
		return false
	}
}

func isProviderMismatchError(err error) bool {
	var runtimeErr *runtime.RuntimeError
	if !errors.As(err, &runtimeErr) {
		return false
	}

	return runtimeErr.Code == runtime.CodeProviderMismatch
}

func resolveFallbackInstallDir() string {
	candidate := strings.TrimSpace(os.Getenv(runtime.InstallDirEnvKey))
	if candidate == "" {
		return runtime.DefaultInstallDir
	}
	return filepath.Clean(candidate)
}

func fallbackTelemtControlEndpoint(runtimeState *runtime.RuntimeInstallation) string {
	port := telemt.DefaultControlAPIPort
	if runtimeState != nil && runtimeState.Env != nil {
		resolved, provided, err := runtimeState.Env.APIPort()
		if err == nil && provided && resolved >= 1 && resolved <= 65535 {
			port = resolved
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func composeDiagnostics(command string, composeRunErr error, stderrSummary string) string {
	parts := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	appendPart := func(value string) {
		trimmed := strings.TrimSpace(redactForCommand(command, value))
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		parts = append(parts, trimmed)
	}

	if composeRunErr != nil {
		appendPart(composeRunErr.Error())
	}
	appendPart(stderrSummary)

	return strings.Join(parts, " | ")
}

func resolveHardenedFallbackEnvPath(installDir string) (string, string, bool, error) {
	resolvedInstallDir, err := resolveAbsolutePathForFallback(installDir)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid fallback install dir %q: %w", installDir, err)
	}

	if err := validatePathChainNoSymlinksForFallback(resolvedInstallDir); err != nil {
		return resolvedInstallDir, "", false, fmt.Errorf("fallback install dir path chain is unsafe: %w", err)
	}
	if err := ensurePathNotSymlinkForFallback(resolvedInstallDir, "fallback install dir"); err != nil {
		return resolvedInstallDir, "", false, err
	}

	installInfo, err := os.Stat(resolvedInstallDir)
	if err != nil {
		return resolvedInstallDir, "", false, fmt.Errorf(
			"fallback install dir %q is not accessible: %w",
			resolvedInstallDir,
			err,
		)
	}
	if !installInfo.IsDir() {
		return resolvedInstallDir, "", false, fmt.Errorf("fallback install dir %q is not a directory", resolvedInstallDir)
	}

	envPath := filepath.Join(resolvedInstallDir, ".env")
	resolvedEnvPath, err := resolveAbsolutePathForFallback(envPath)
	if err != nil {
		return resolvedInstallDir, "", false, fmt.Errorf("invalid fallback env path %q: %w", envPath, err)
	}
	if !isPathWithinFallbackInstallDir(resolvedInstallDir, resolvedEnvPath) {
		return resolvedInstallDir, "", false, fmt.Errorf(
			"fallback env path %q escapes install dir %q",
			resolvedEnvPath,
			resolvedInstallDir,
		)
	}
	if err := validatePathChainNoSymlinksForFallback(resolvedEnvPath); err != nil {
		return resolvedInstallDir, "", false, fmt.Errorf("fallback env path chain is unsafe: %w", err)
	}
	envInfo, err := os.Lstat(resolvedEnvPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolvedInstallDir, "", false, nil
		}
		return resolvedInstallDir, "", false, fmt.Errorf(
			"fallback env file %q is not accessible: %w",
			resolvedEnvPath,
			err,
		)
	}
	if envInfo.Mode()&os.ModeSymlink != 0 {
		return resolvedInstallDir, "", false, fmt.Errorf(
			"fallback env file %q must not be a symlink",
			resolvedEnvPath,
		)
	}
	if envInfo.IsDir() {
		return resolvedInstallDir, "", false, fmt.Errorf("fallback env file %q is a directory", resolvedEnvPath)
	}

	return resolvedInstallDir, resolvedEnvPath, true, nil
}

func resolveAbsolutePathForFallback(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func validatePathChainNoSymlinksForFallback(path string) error {
	resolved, err := resolveAbsolutePathForFallback(path)
	if err != nil {
		return err
	}

	volume := filepath.VolumeName(resolved)
	segments := splitPathSegmentsForFallback(resolved, volume)
	current := volume
	if current == "" {
		if filepath.IsAbs(resolved) {
			current = string(os.PathSeparator)
		} else {
			current = "."
		}
	} else {
		current = filepath.Clean(volume + string(os.PathSeparator))
	}

	for index, segment := range segments {
		next := filepath.Join(current, segment)
		info, lstatErr := os.Lstat(next)
		if lstatErr != nil {
			if errors.Is(lstatErr, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("path component %q is not accessible: %w", next, lstatErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q must not be a symlink", next)
		}
		if index < len(segments)-1 && !info.IsDir() {
			return fmt.Errorf("path component %q is not a directory", next)
		}
		current = next
	}

	return nil
}

func splitPathSegmentsForFallback(path string, volume string) []string {
	trimmed := strings.TrimPrefix(path, volume)
	trimmed = strings.TrimPrefix(trimmed, string(os.PathSeparator))
	if strings.TrimSpace(trimmed) == "" {
		return nil
	}

	parts := strings.Split(trimmed, string(os.PathSeparator))
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		segments = append(segments, part)
	}
	return segments
}

func ensurePathNotSymlinkForFallback(path string, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s %q is not accessible: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	return nil
}

func isPathWithinFallbackInstallDir(base string, target string) bool {
	baseResolved, err := resolveAbsolutePathForFallback(base)
	if err != nil {
		return false
	}
	targetResolved, err := resolveAbsolutePathForFallback(target)
	if err != nil {
		return false
	}

	relative, err := filepath.Rel(baseResolved, targetResolved)
	if err != nil {
		return false
	}
	cleanRel := filepath.Clean(relative)
	if cleanRel == "." {
		return true
	}
	if cleanRel == ".." {
		return false
	}
	return !strings.HasPrefix(cleanRel, ".."+string(os.PathSeparator))
}

func requireNoArgs(command string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("%s command does not accept arguments", command)
}

func writeCommandOutput(writer io.Writer, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	_, err := io.WriteString(writer, trimmed+"\n")
	return err
}

func logTelemtFinalSummary(
	logger *slog.Logger,
	commandName string,
	summary telemt.StatusSummary,
) {
	args := []any{
		"command", commandName,
		"provider", summary.Provider,
		"runtime_status", summary.RuntimeStatus,
		"compose_state", summary.ComposeStatus,
		"health_state", summary.HealthStatus,
		"link_state", summary.LinkStatus,
		"link_available", summary.LinkAvailable(),
		"degraded_reasons", summary.DegradedReason,
	}
	if summary.RuntimeStatus == telemt.RuntimeStatusHealthy {
		logger.Info("final runtime summary", args...)
		return
	}
	logger.Warn("final runtime summary", args...)
}

func logUnsupportedFinalSummary(
	logger *slog.Logger,
	commandName string,
	summary output.UnsupportedProviderSummary,
) {
	logger.Warn(
		"final runtime summary",
		"command", commandName,
		"provider", summary.Provider,
		"runtime_status", "partial",
		"compose_checked", summary.ComposeChecked,
		"compose_state", summary.ComposeStatus,
		"compose_reason", summary.ComposeReason,
		"runtime_error_present", strings.TrimSpace(summary.RuntimeResolveError) != "",
	)
}
