package runtime

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInstallDirPreservesPOSIXRootedDefaultPath(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	resolved := resolveInstallDir(LoadOptions{
		InstallDir: DefaultInstallDir,
		Logger:     logger,
	}, logger)
	if resolved != DefaultInstallDir {
		t.Fatalf("expected POSIX-rooted default install dir to stay stable, got %q", resolved)
	}

	if !strings.Contains(logs.String(), "mode=posix-rooted") {
		t.Fatalf("expected posix-rooted normalization log, got: %s", logs.String())
	}
}

func TestLoadRejectsSymlinkedEnvFileDuringPathHardening(t *testing.T) {
	installDir := t.TempDir()
	mustWriteRuntimeFixtureFiles(t, installDir, "PROVIDER=telemt\n")

	targetEnvPath := filepath.Join(installDir, "runtime-target.env")
	if err := os.WriteFile(targetEnvPath, []byte("PROVIDER=telemt\n"), 0o600); err != nil {
		t.Fatalf("failed to write env symlink target: %v", err)
	}

	envPath := filepath.Join(installDir, ".env")
	if err := os.Remove(envPath); err != nil {
		t.Fatalf("failed to remove fixture env file: %v", err)
	}
	if err := os.Symlink(targetEnvPath, envPath); err != nil {
		t.Skipf("symlink creation is unavailable in this environment: %v", err)
	}

	_, err := Load(LoadOptions{
		InstallDir: installDir,
	})
	if err == nil {
		t.Fatalf("expected runtime load to fail for symlinked env file")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeInstallDirInvalid {
		t.Fatalf("expected install_dir_invalid, got %s", runtimeErr.Code)
	}
	if !strings.Contains(err.Error(), "runtime env file must not be a symlink") {
		t.Fatalf("expected symlink hardening error for env file, got: %v", err)
	}
}

func TestLoadReturnsInstallDirMissingWhenInstallDirDoesNotExist(t *testing.T) {
	baseDir := t.TempDir()
	missingInstallDir := filepath.Join(baseDir, "missing-install-dir")

	_, err := Load(LoadOptions{
		InstallDir: missingInstallDir,
	})
	if err == nil {
		t.Fatalf("expected runtime load to fail for missing install dir")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeInstallDirMissing {
		t.Fatalf("expected install_dir_missing, got %s", runtimeErr.Code)
	}
	if !strings.Contains(err.Error(), "install dir missing") {
		t.Fatalf("expected install dir missing message, got: %v", err)
	}
	if strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("expected missing-install-dir error, got symlink hardening error: %v", err)
	}
}

func TestLoadRejectsSymlinkedProviderConfigDuringPathHardening(t *testing.T) {
	installDir := t.TempDir()
	mustWriteRuntimeFixtureFiles(t, installDir, "PROVIDER=telemt\n")

	providerDir := filepath.Join(installDir, "providers", string(ProviderTelemt))
	targetConfigPath := filepath.Join(providerDir, "telemt-target.toml")
	if err := os.WriteFile(targetConfigPath, []byte("# target config\n"), 0o600); err != nil {
		t.Fatalf("failed to write provider config symlink target: %v", err)
	}

	configPath := filepath.Join(providerDir, "telemt.toml")
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("failed to remove fixture provider config: %v", err)
	}
	if err := os.Symlink(targetConfigPath, configPath); err != nil {
		t.Skipf("symlink creation is unavailable in this environment: %v", err)
	}

	_, err := Load(LoadOptions{
		InstallDir: installDir,
	})
	if err == nil {
		t.Fatalf("expected runtime load to fail for symlinked provider config")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeInstallDirInvalid {
		t.Fatalf("expected install_dir_invalid, got %s", runtimeErr.Code)
	}
	if !strings.Contains(err.Error(), "telemt provider config file must not be a symlink") {
		t.Fatalf("expected symlink hardening error for provider config, got: %v", err)
	}
}

func TestLoadEmitsRedactedEnvSnapshotInRuntimeLogs(t *testing.T) {
	installDir := t.TempDir()
	mustWriteRuntimeFixtureFiles(
		t,
		installDir,
		strings.Join([]string{
			"PROVIDER=telemt",
			"SECRET=supersecret",
			"STARTUP_LINK=tg://proxy?server=127.0.0.1&port=443&secret=abcdef",
			"",
		}, "\n"),
	)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	runtimeState, err := Load(LoadOptions{
		InstallDir: installDir,
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("expected runtime load success, got %v", err)
	}
	if runtimeState.Provider.Name != ProviderTelemt {
		t.Fatalf("expected provider telemt, got %q", runtimeState.Provider.Name)
	}

	logText := logs.String()
	if !strings.Contains(logText, "runtime env loaded") {
		t.Fatalf("expected runtime env loaded log, got: %s", logText)
	}
	if !strings.Contains(logText, "runtime discovery resolved") {
		t.Fatalf("expected runtime discovery log, got: %s", logText)
	}
	if strings.Contains(logText, "supersecret") {
		t.Fatalf("expected secret values to stay redacted in logs, got: %s", logText)
	}
	if strings.Contains(logText, "tg://proxy?server=127.0.0.1&port=443&secret=abcdef") {
		t.Fatalf("expected proxy links to stay redacted in logs, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted]") {
		t.Fatalf("expected secret redaction marker in logs, got: %s", logText)
	}
	if !strings.Contains(logText, "[redacted-proxy-link]") {
		t.Fatalf("expected proxy-link redaction marker in logs, got: %s", logText)
	}
}

func mustWriteRuntimeFixtureFiles(t *testing.T, installDir string, envContent string) {
	t.Helper()

	envPath := filepath.Join(installDir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		t.Fatalf("failed to write fixture env file: %v", err)
	}

	composePath := filepath.Join(installDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  telemt:\n    image: telemt/telemt\n"), 0o600); err != nil {
		t.Fatalf("failed to write fixture compose file: %v", err)
	}

	telemtDir := filepath.Join(installDir, "providers", string(ProviderTelemt))
	if err := os.MkdirAll(telemtDir, 0o755); err != nil {
		t.Fatalf("failed to create telemt provider dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(telemtDir, "telemt.toml"), []byte("# telemt config\n"), 0o600); err != nil {
		t.Fatalf("failed to write telemt provider config: %v", err)
	}
}
