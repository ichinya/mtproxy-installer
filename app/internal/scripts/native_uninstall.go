package scripts

import (
	"context"
	"fmt"
	"os"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

func (m *Manager) uninstallGoNative(
	ctx context.Context,
	runtimeState *runtime.RuntimeInstallation,
	resolvedProvider runtime.Provider,
	keepData bool,
	envOverrides map[string]string,
) (execadapter.Result, error) {
	transcript := newLifecycleTranscript()
	resultArgs := []string{"uninstall", string(resolvedProvider)}

	dockerPath, err := m.runLifecyclePreflight(ctx, "uninstall", runtimeState.Paths.InstallDir, envOverrides)
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	transcript.stdoutLine("==============================")
	transcript.stdoutLine("MTProxy Uninstaller (v1 telemt-only)")
	transcript.stdoutLine("==============================")
	transcript.stdoutLine("")
	transcript.stdoutLine(fmt.Sprintf("Install dir: %s", runtimeState.Paths.InstallDir))
	transcript.stdoutLine(fmt.Sprintf("Strategy: %s", UninstallStrategyTelemtOnly))
	transcript.stdoutLine(fmt.Sprintf("Provider: %s", resolvedProvider))
	transcript.stdoutLine(fmt.Sprintf("Keep data: %t", keepData))

	if err := m.validateUninstallRuntimeTrust(runtimeState, resolvedProvider); err != nil {
		transcript.stdoutLine("Cleanup status: failed_preflight")
		transcript.stdoutLine("Data removed: false")
		transcript.stdoutLine("Image cleanup: skipped")
		transcript.stdoutLine(fmt.Sprintf("Outcome: %s", err.Error()))
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	transcript.stdoutLine("WARN: Destructive action requested: stop runtime, remove images, and apply KEEP_DATA policy")

	cleanupStatus := UninstallCleanupStatusCompleted
	dataRemoved := "false"
	imageCleanup := "unknown"
	operatorOutcome := ""

	downResult, downErr := m.runComposeCommand(ctx, dockerPath, runtimeState, envOverrides, "down", "--remove-orphans")
	transcript.appendResult(downResult)
	if downErr != nil {
		cleanupStatus = UninstallCleanupStatusPartial
		imageCleanup = "skipped"
		operatorOutcome = "Telemt cleanup finished with partial removal"
		transcript.stderrLine("ERROR: docker compose down failed for runtime compose contract")
		transcript.stdoutLine("Hint: compose teardown failed; skipping image and data removal")
	} else {
		transcript.stdoutLine("Hint: compose stack stopped")

		imageCleanup, err = m.removeRuntimeImages(ctx, dockerPath, runtimeState, envOverrides, transcript)
		if err != nil {
			cleanupStatus = UninstallCleanupStatusPartial
			operatorOutcome = "Telemt cleanup finished with partial removal"
		}

		if keepData {
			dataRemoved = "false"
			transcript.stdoutLine("Hint: keeping installation directory (KEEP_DATA=true)")
		} else {
			if err := os.RemoveAll(runtimeState.Paths.InstallDir); err != nil {
				cleanupStatus = UninstallCleanupStatusPartial
				operatorOutcome = "Telemt cleanup finished with partial removal"
				transcript.stderrLine(fmt.Sprintf("ERROR: Failed to remove installation directory %s", runtimeState.Paths.InstallDir))
			} else {
				dataRemoved = "true"
			}
		}
	}

	if cleanupStatus != UninstallCleanupStatusPartial {
		if keepData {
			cleanupStatus = UninstallCleanupStatusCompletedKeepData
			operatorOutcome = "Telemt runtime removed; install directory preserved"
		} else {
			cleanupStatus = UninstallCleanupStatusCompleted
			operatorOutcome = "Telemt runtime removed; install directory deleted"
		}
	}

	transcript.stdoutLine(fmt.Sprintf("Cleanup status: %s", cleanupStatus))
	transcript.stdoutLine(fmt.Sprintf("Data removed: %s", dataRemoved))
	transcript.stdoutLine(fmt.Sprintf("Image cleanup: %s", imageCleanup))
	transcript.stdoutLine(fmt.Sprintf("Outcome: %s", operatorOutcome))

	if cleanupStatus == UninstallCleanupStatusPartial {
		transcript.stderrLine("ERROR: Uninstall completed with partial cleanup. Review markers above.")
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, fmt.Errorf("uninstall lifecycle reported partial cleanup"))
	}

	return transcript.result(resultArgs, 0), nil
}

func (m *Manager) validateUninstallRuntimeTrust(
	runtimeState *runtime.RuntimeInstallation,
	provider runtime.Provider,
) error {
	requiredPaths := []string{
		runtimeState.Paths.InstallDir,
		runtimeState.Paths.EnvFile,
		runtimeState.Paths.ComposeFile,
		runtimeState.Provider.ConfigPath,
	}
	for _, path := range requiredPaths {
		if err := ensurePathOwnershipTrusted(path); err != nil {
			return newLifecyclePreflightError(
				"uninstall",
				"ownership",
				fmt.Sprintf("untrusted owner detected for %s", path),
				"restore root ownership for runtime files before retrying, for example: sudo chown root:root <path>",
				err,
			)
		}
		if err := ensurePathPermissionsTrusted(path); err != nil {
			return newLifecyclePreflightError(
				"uninstall",
				"permissions",
				fmt.Sprintf("unsafe permissions detected for %s", path),
				"remove group/other write bits before retrying, for example: sudo chmod go-w <path>",
				err,
			)
		}
	}

	marker, ok := runtimeComposeMarkers[provider]
	if !ok {
		return newLifecyclePreflightError(
			"uninstall",
			"provider_contract",
			fmt.Sprintf("missing compose marker contract for provider %q", provider),
			"restore the expected runtime contract for the detected provider before retrying uninstall",
			nil,
		)
	}
	composeBody, err := os.ReadFile(runtimeState.Paths.ComposeFile)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(string(composeBody)), strings.ToLower(marker)) {
		return newLifecyclePreflightError(
			"uninstall",
			"provider_contract",
			fmt.Sprintf("compose provider marker mismatch: expected %q for provider %q", marker, provider),
			"restore the matching compose/provider contract before retrying uninstall",
			nil,
		)
	}
	return nil
}

func (m *Manager) removeRuntimeImages(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
	transcript *lifecycleTranscript,
) (string, error) {
	images, err := m.collectComposeImages(ctx, dockerPath, runtimeState, envOverrides)
	if err != nil {
		transcript.stderrLine("ERROR: " + err.Error())
		return "failed", err
	}
	if len(images) == 0 {
		transcript.stdoutLine("WARN: No telemt runtime images detected from compose contract")
		return "not_found", nil
	}

	removedAny := false
	failedAny := false
	for _, image := range images {
		if !isValidLifecycleImageRef(image) {
			failedAny = true
			transcript.stderrLine(fmt.Sprintf("ERROR: Skipping invalid image reference candidate: %s", image))
			continue
		}

		inspectResult, inspectErr := m.runDockerCommand(ctx, dockerPath, runtimeState.Paths.InstallDir, envOverrides, "image", "inspect", "--", image)
		if inspectErr != nil {
			_ = inspectResult
			continue
		}

		removeResult, removeErr := m.runDockerCommand(ctx, dockerPath, runtimeState.Paths.InstallDir, envOverrides, "rmi", "--", image)
		transcript.appendResult(removeResult)
		if removeErr != nil {
			failedAny = true
			transcript.stderrLine(fmt.Sprintf("ERROR: Failed to remove image %s", image))
			continue
		}

		removedAny = true
		transcript.stdoutLine(fmt.Sprintf("Hint: removed image %s", image))
	}

	if failedAny {
		return "failed", fmt.Errorf("one or more runtime images could not be removed")
	}
	if removedAny {
		return "removed", nil
	}
	return "not_found", nil
}

func (m *Manager) collectComposeImages(
	ctx context.Context,
	dockerPath string,
	runtimeState *runtime.RuntimeInstallation,
	envOverrides map[string]string,
) ([]string, error) {
	args := []string{
		"compose",
		"-f", runtimeState.Paths.ComposeFile,
		"--project-directory", runtimeState.Paths.InstallDir,
		"--env-file", runtimeState.Paths.EnvFile,
		"config",
		"--images",
	}
	result, err := m.runDockerCommand(ctx, dockerPath, runtimeState.Paths.InstallDir, envOverrides, args...)
	if err != nil {
		return nil, err
	}

	images := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, line := range splitLifecycleLines(result.Stdout) {
		image := strings.TrimSpace(line)
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		images = append(images, image)
	}
	return images, nil
}

func isValidLifecycleImageRef(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "-") {
		return false
	}
	if strings.Contains(trimmed, "..") {
		return false
	}
	return imageReferencePattern.MatchString(trimmed)
}
