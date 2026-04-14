package scripts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

type providerImageContract struct {
	imageKey      string
	sourceKey     string
	defaultSource string
}

func (m *Manager) updateGoNative(
	ctx context.Context,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
) (execadapter.Result, error) {
	transcript := newLifecycleTranscript()
	resultArgs := []string{"update", string(runtimeState.Provider.Name)}

	dockerPath, err := m.runLifecyclePreflight(ctx, "update", runtimeState.Paths.InstallDir, envOverrides)
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	contract, err := providerImageContractFor(runtimeState.Provider.Name)
	if err != nil {
		transcript.stderrLine("Error: " + err.Error())
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	currentImage, sourceRef := currentRuntimeImageState(runtimeState, contract)
	transcript.stdoutLine("==============================")
	transcript.stdoutLine("MTProxy Provider Updater")
	transcript.stdoutLine("==============================")
	transcript.stdoutLine("")
	transcript.stdoutLine(fmt.Sprintf("Install dir: %s", runtimeState.Paths.InstallDir))
	transcript.stdoutLine(fmt.Sprintf("Provider: %s", runtimeState.Provider.Name))
	transcript.stdoutLine(fmt.Sprintf("Configured source: %s", sourceRef))
	transcript.stdoutLine("")

	if err := upsertEnvFileValue(runtimeState.Paths.EnvFile, "PROVIDER", string(runtimeState.Provider.Name)); err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}
	if err := upsertEnvFileValue(runtimeState.Paths.EnvFile, contract.sourceKey, sourceRef); err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	backupImage, backupErr := m.createBackupImage(ctx, dockerPath, runtimeState, envOverrides)
	if backupErr != nil {
		m.logger.Warn("update backup image preparation failed", "provider", runtimeState.Provider.Name, "error", execadapter.RedactText(backupErr.Error()))
	}
	rollbackImage := currentImage
	if backupImage != "" {
		rollbackImage = backupImage
		transcript.stdoutLine(fmt.Sprintf("Prepared rollback image: %s", rollbackImage))
	}

	targetImage, err := m.resolveImageReference(ctx, dockerPath, sourceRef, envOverrides)
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	if runtimeState.Provider.Name == runtime.ProviderMTG {
		cfg := installNativeConfig{
			providerDir: filepath.Join(runtimeState.Paths.InstallDir, "providers", string(runtime.ProviderMTG)),
			mtgImage:    targetImage,
			installDir:  runtimeState.Paths.InstallDir,
		}
		if _, err := m.validateMTGConfig(ctx, dockerPath, cfg, envOverrides); err != nil {
			writeLifecycleFailure(transcript, err)
			result := transcript.result(resultArgs, 1)
			return result, lifecycleCommandError(result, err)
		}
	}

	if currentImage != "" && targetImage == currentImage {
		transcript.stdoutLine(fmt.Sprintf("Image is already up to date: %s", currentImage))
		if err := m.validateRunningProvider(ctx, dockerPath, runtimeState, envOverrides); err != nil {
			transcript.stderrLine("Error: Current provider is not healthy. Update aborted.")
			result := transcript.result(resultArgs, 1)
			return result, lifecycleCommandError(result, err)
		}
		writeUpdateSummary(transcript, runtimeState, sourceRef, currentImage)
		return transcript.result(resultArgs, 0), nil
	}

	if err := upsertEnvFileValue(runtimeState.Paths.EnvFile, contract.imageKey, targetImage); err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	upResult, upErr := m.runComposeCommand(
		ctx,
		dockerPath,
		runtimeState,
		envOverrides,
		"up",
		"-d",
		"--force-recreate",
		string(runtimeState.Provider.Name),
	)
	transcript.appendResult(upResult)
	if upErr != nil {
		return m.rollbackUpdate(
			ctx,
			transcript,
			resultArgs,
			dockerPath,
			runtimeState,
			envOverrides,
			contract.imageKey,
			rollbackImage,
			fmt.Errorf("update failed while restarting the provider"),
			fmt.Sprintf("Update failed while restarting the provider. Previous image restored."),
		)
	}

	if err := m.validateRunningProvider(ctx, dockerPath, runtimeState, envOverrides); err != nil {
		return m.rollbackUpdate(
			ctx,
			transcript,
			resultArgs,
			dockerPath,
			runtimeState,
			envOverrides,
			contract.imageKey,
			rollbackImage,
			err,
			"Update failed validation. Previous image restored.",
		)
	}

	writeUpdateSummary(transcript, runtimeState, sourceRef, targetImage)
	return transcript.result(resultArgs, 0), nil
}

func providerImageContractFor(provider runtime.Provider) (providerImageContract, error) {
	switch provider {
	case runtime.ProviderTelemt:
		return providerImageContract{
			imageKey:      "TELEMT_IMAGE",
			sourceKey:     "TELEMT_IMAGE_SOURCE",
			defaultSource: defaultTelemtImageSource,
		}, nil
	case runtime.ProviderMTG:
		return providerImageContract{
			imageKey:      "MTG_IMAGE",
			sourceKey:     "MTG_IMAGE_SOURCE",
			defaultSource: defaultMTGImageSource,
		}, nil
	default:
		return providerImageContract{}, fmt.Errorf("unsupported provider for native update: %s", provider)
	}
}

func currentRuntimeImageState(runtimeState *runtime.RuntimeInstallation, contract providerImageContract) (string, string) {
	currentImage := ""
	sourceRef := ""

	switch runtimeState.Provider.Name {
	case runtime.ProviderTelemt:
		currentImage = runtimeState.Env.TelemtImage()
		sourceRef = runtimeState.Env.TelemtImageSource()
	case runtime.ProviderMTG:
		currentImage = runtimeState.Env.MTGImage()
		sourceRef = runtimeState.Env.MTGImageSource()
	}

	if sourceRef == "" {
		if currentImage != "" {
			sourceRef = currentImage
		} else {
			sourceRef = contract.defaultSource
		}
	}

	return currentImage, sourceRef
}

func (m *Manager) createBackupImage(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
) (string, error) {
	containerID, err := m.composeServiceContainerID(ctx, dockerPath, runtimeState, envOverrides, string(runtimeState.Provider.Name))
	if err != nil || containerID == "" {
		return "", err
	}

	result, err := m.runDockerCommand(ctx, dockerPath, runtimeState.Paths.InstallDir, envOverrides, "inspect", "--format", "{{.Image}}", containerID)
	if err != nil {
		return "", err
	}
	imageID := firstNonEmptyLine(result.Stdout)
	if imageID == "" {
		return "", nil
	}

	backupRef := fmt.Sprintf("mtproxy-installer/%s-backup:%s", runtimeState.Provider.Name, time.Now().UTC().Format("20060102150405"))
	_, err = m.runDockerCommand(ctx, dockerPath, runtimeState.Paths.InstallDir, envOverrides, "image", "tag", imageID, backupRef)
	if err != nil {
		return "", err
	}
	return backupRef, nil
}

func (m *Manager) composeServiceContainerID(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
	service string,
) (string, error) {
	result, err := m.runComposeCommand(ctx, dockerPath, runtimeState, envOverrides, "ps", "-q", service)
	if err != nil {
		return "", err
	}
	return firstNonEmptyLine(result.Stdout), nil
}

func (m *Manager) validateRunningProvider(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
) error {
	switch runtimeState.Provider.Name {
	case runtime.ProviderTelemt:
		_, err := m.waitForTelemtUsers(ctx, runtimeState)
		return err
	case runtime.ProviderMTG:
		return m.waitForMTGRunning(ctx, dockerPath, runtimeState, envOverrides)
	default:
		return fmt.Errorf("unsupported provider for validation: %s", runtimeState.Provider.Name)
	}
}

func (m *Manager) waitForMTGRunning(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
) error {
	var lastErr error
	for attempt := 0; attempt < defaultInstallPollAttempts; attempt++ {
		containerID, err := m.composeServiceContainerID(ctx, dockerPath, runtimeState, envOverrides, "mtg")
		if err == nil && containerID != "" {
			result, inspectErr := m.runDockerCommand(
				ctx,
				dockerPath,
				runtimeState.Paths.InstallDir,
				envOverrides,
				"inspect",
				"--format",
				"{{.State.Status}}",
				containerID,
			)
			if inspectErr == nil {
				status := firstNonEmptyLine(result.Stdout)
				switch status {
				case "running":
					return nil
				case "exited", "dead":
					return fmt.Errorf("mtg container state is %s", status)
				}
			} else {
				lastErr = inspectErr
			}
		} else if err != nil {
			lastErr = err
		}

		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(defaultInstallPollDelay)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mtg container did not become ready")
	}
	return lastErr
}

func (m *Manager) rollbackUpdate(
	ctx context.Context,
	transcript *lifecycleTranscript,
	resultArgs []string,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
	imageKey string,
	rollbackImage string,
	cause error,
	finalMessage string,
) (execadapter.Result, error) {
	if rollbackImage == "" {
		transcript.stderrLine("Error: Update failed and no rollback image is available.")
		result := transcript.result(resultArgs, 1)
		if cause == nil {
			cause = fmt.Errorf("update failed and no rollback image is available")
		}
		return result, lifecycleCommandError(result, cause)
	}

	transcript.stdoutLine(fmt.Sprintf("Rolling back %s to %s", runtimeState.Provider.Name, rollbackImage))
	if err := upsertEnvFileValue(runtimeState.Paths.EnvFile, imageKey, rollbackImage); err != nil {
		transcript.stderrLine("Error: Rollback failed. Manual recovery required.")
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	upResult, upErr := m.runComposeCommand(
		ctx,
		dockerPath,
		runtimeState,
		envOverrides,
		"up",
		"-d",
		"--force-recreate",
		string(runtimeState.Provider.Name),
	)
	transcript.appendResult(upResult)
	if upErr != nil {
		transcript.stderrLine("Error: Rollback failed. Manual recovery required.")
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, upErr)
	}

	if err := m.validateRunningProvider(ctx, dockerPath, runtimeState, envOverrides); err != nil {
		transcript.stderrLine("Error: Rollback failed. Manual recovery required.")
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	transcript.stderrLine("Error: " + finalMessage)
	result := transcript.result(resultArgs, 1)
	if cause == nil {
		cause = errors.New(strings.TrimSuffix(finalMessage, "."))
	}
	return result, lifecycleCommandError(result, cause)
}

func writeUpdateSummary(
	transcript *lifecycleTranscript,
	runtimeState *runtime.RuntimeInstallation,
	sourceRef string,
	imageRef string,
) {
	transcript.stdoutLine("")
	transcript.stdoutLine("==============================")
	transcript.stdoutLine(fmt.Sprintf("%s update complete", runtimeState.Provider.Name))
	transcript.stdoutLine("==============================")
	transcript.stdoutLine("")
	transcript.stdoutLine(fmt.Sprintf("Provider: %s", runtimeState.Provider.Name))
	transcript.stdoutLine(fmt.Sprintf("Source: %s", sourceRef))
	transcript.stdoutLine(fmt.Sprintf("Active: %s", imageRef))

	switch runtimeState.Provider.Name {
	case runtime.ProviderTelemt:
		apiPort, _, err := runtimeState.Env.APIPort()
		if err != nil || apiPort <= 0 {
			apiPort = defaultTelemtAPIPort
		}
		transcript.stdoutLine(fmt.Sprintf("Health: curl http://127.0.0.1:%d/v1/health", apiPort))
	case runtime.ProviderMTG:
		transcript.stdoutLine(fmt.Sprintf(
			"Logs: docker compose -f %s --project-directory %s --env-file %s logs -f mtg",
			runtimeState.Paths.ComposeFile,
			runtimeState.Paths.InstallDir,
			runtimeState.Paths.EnvFile,
		))
	}
}
