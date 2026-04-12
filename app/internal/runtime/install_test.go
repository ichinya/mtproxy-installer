package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
