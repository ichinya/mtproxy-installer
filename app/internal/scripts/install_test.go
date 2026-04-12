package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/runtime"
)

func TestSanitizeInstallEnvValueMapRejectsConfigInjection(t *testing.T) {
	env := map[string]string{
		"TLS_DOMAIN": "example.com\nINJECT=1",
	}

	err := sanitizeInstallEnvValueMap(env)
	if err == nil {
		t.Fatalf("expected sanitizeInstallEnvValueMap to reject newline injection")
	}
}

func TestSanitizeInstallEnvValueMapAcceptsSafeValues(t *testing.T) {
	env := map[string]string{
		"TLS_DOMAIN":          "proxy.example.com",
		"PUBLIC_IP":           "203.0.113.42",
		"SECRET":              "abcdef0123456789abcdef0123456789",
		"PROXY_USER":          "operator-1",
		"TELEMT_IMAGE":        "ghcr.io/example/telemt:1.2.3",
		"TELEMT_IMAGE_SOURCE": "ghcr.io/example/telemt:stable",
	}

	if err := sanitizeInstallEnvValueMap(env); err != nil {
		t.Fatalf("expected sanitizeInstallEnvValueMap to accept safe values, got %v", err)
	}
}

func TestSanitizeInstallEnvValueMapRejectsProxyUserInjection(t *testing.T) {
	env := map[string]string{
		"PROXY_USER": "user\nINJECT=1",
	}

	err := sanitizeInstallEnvValueMap(env)
	if err == nil {
		t.Fatalf("expected sanitizeInstallEnvValueMap to reject PROXY_USER newline injection")
	}
}

func TestValidatePathChainNoSymlinksRejectsSymlinkParents(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "target")
	linkDir := filepath.Join(tempDir, "link")

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Skipf("symlink creation is unavailable in this environment: %v", err)
	}

	err := validatePathChainNoSymlinks(filepath.Join(linkDir, "runtime"))
	if err == nil {
		t.Fatalf("expected validatePathChainNoSymlinks to reject symlink in path chain")
	}
}

func TestEnforceInstallDirPathSafetyRejectsSymlinkPath(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "target")
	linkDir := filepath.Join(tempDir, "link")

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Skipf("symlink creation is unavailable in this environment: %v", err)
	}

	err := enforceInstallDirPathSafety(filepath.Join(linkDir, "runtime"), true)
	if err == nil {
		t.Fatalf("expected enforceInstallDirPathSafety to reject symlink path")
	}
}

func TestMergeAllowedEnvKeysIncludesInstallAndRuntimeVars(t *testing.T) {
	merged := mergeAllowedEnvKeys(privilegedExecutionEnvOptInAllowlist, installEnvOverrideAllowlist)
	expected := []string{"INSTALL_DIR", "HTTP_PROXY", "DOCKER_HOST", "PROVIDER"}

	for _, key := range expected {
		found := false
		for _, candidate := range merged {
			if candidate == key {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected merged allowlist to include %q", key)
		}
	}
}

func TestSanitizeEnvOverridesAllowsExplicitRuntimeAndDockerOptIn(t *testing.T) {
	sanitized, err := sanitizeEnvOverrides("update", map[string]string{
		"DOCKER_TLS_VERIFY":    "1",
		"COMPOSE_PROJECT_NAME": "mtproxy",
		"HOME":                 "/root",
		"XDG_RUNTIME_DIR":      "/run/user/0",
		"INSTALL_DIR":          "/opt/mtproxy-installer",
	}, updateEnvOverrideAllowlist, envOverrideTrustPolicy{
		AllowTrustBoundaryEnv: true,
		PrivilegedContext:     "",
	})
	if err != nil {
		t.Fatalf("expected update env overrides with explicit opt-in keys to pass, got %v", err)
	}

	if got := sanitized["DOCKER_TLS_VERIFY"]; got != "1" {
		t.Fatalf("unexpected DOCKER_TLS_VERIFY value %q", got)
	}
	if got := sanitized["COMPOSE_PROJECT_NAME"]; got != "mtproxy" {
		t.Fatalf("unexpected COMPOSE_PROJECT_NAME value %q", got)
	}
}

func TestSanitizeEnvOverridesAllowsDockerTLSPrefixOptIn(t *testing.T) {
	sanitized, err := sanitizeEnvOverrides("update", map[string]string{
		"DOCKER_TLS_CERTDIR": "/certs",
		"INSTALL_DIR":        "/opt/mtproxy-installer",
	}, updateEnvOverrideAllowlist, envOverrideTrustPolicy{
		AllowTrustBoundaryEnv: true,
		PrivilegedContext:     "",
	})
	if err != nil {
		t.Fatalf("expected DOCKER_TLS* opt-in key to pass validation, got %v", err)
	}

	if got := sanitized["DOCKER_TLS_CERTDIR"]; got != "/certs" {
		t.Fatalf("unexpected DOCKER_TLS_CERTDIR value %q", got)
	}
}

func TestSanitizeEnvOverridesRejectsTrustBoundaryEnvWithoutAllowFlag(t *testing.T) {
	_, err := sanitizeEnvOverrides("update", map[string]string{
		"DOCKER_HOST": "tcp://docker.internal:2375",
		"INSTALL_DIR": "/opt/mtproxy-installer",
	}, updateEnvOverrideAllowlist, envOverrideTrustPolicy{})
	if err == nil {
		t.Fatalf("expected trust-boundary env override to be rejected without explicit opt-in")
	}
	if !strings.Contains(err.Error(), "require explicit allow flag") {
		t.Fatalf("expected allow flag hint, got %v", err)
	}
}

func TestSanitizeEnvOverridesRejectsTrustBoundaryEnvInPrivilegedContext(t *testing.T) {
	_, err := sanitizeEnvOverrides("update", map[string]string{
		"COMPOSE_PROJECT_NAME": "mtproxy",
		"INSTALL_DIR":          "/opt/mtproxy-installer",
	}, updateEnvOverrideAllowlist, envOverrideTrustPolicy{
		AllowTrustBoundaryEnv: true,
		PrivilegedContext:     "ci",
	})
	if err == nil {
		t.Fatalf("expected trust-boundary env override to be rejected in privileged context")
	}
	if !strings.Contains(err.Error(), "blocked in privileged context") {
		t.Fatalf("expected privileged context marker, got %v", err)
	}
}

func TestEnforceInstallDirDestructivePolicyRequiresExplicitOverride(t *testing.T) {
	if err := enforceInstallDirDestructivePolicy("install", runtime.DefaultInstallDir, false); err != nil {
		t.Fatalf("expected default install dir to pass without override, got %v", err)
	}

	err := enforceInstallDirDestructivePolicy("install", "/opt/mtproxy-custom", false)
	if err == nil {
		t.Fatalf("expected non-default install dir to require explicit override")
	}
	if !strings.Contains(err.Error(), "AllowNonDefaultInstallDir=true") {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := enforceInstallDirDestructivePolicy("install", "/opt/mtproxy-custom", true); err != nil {
		t.Fatalf("expected non-default install dir with explicit override to pass, got %v", err)
	}
}

func TestEnforceInstallDirDestructivePolicyRequiresExplicitOverrideForAllScriptFlows(t *testing.T) {
	operations := []string{"install", "update", "uninstall"}
	for _, operation := range operations {
		err := enforceInstallDirDestructivePolicy(operation, "/opt/mtproxy-custom", false)
		if err == nil {
			t.Fatalf("expected %s to require explicit override for non-default install dir", operation)
		}
		if !strings.Contains(err.Error(), "AllowNonDefaultInstallDir=true") {
			t.Fatalf("unexpected %s error: %v", operation, err)
		}
	}
}

func TestEnforceInstallDirDestructivePolicyRejectsInstallDirOutsideAllowedRoots(t *testing.T) {
	operations := []string{"install", "update", "uninstall"}
	for _, operation := range operations {
		err := enforceInstallDirDestructivePolicy(operation, "/tmp/mtproxy-custom", true)
		if err == nil {
			t.Fatalf("expected %s to reject install dir outside allowed roots", operation)
		}
		if !strings.Contains(err.Error(), "allowed install roots") {
			t.Fatalf("unexpected %s error: %v", operation, err)
		}
	}
}

func TestEnforceInstallDirDestructivePolicyRejectsProtectedSystemPathEvenWithOverride(t *testing.T) {
	operations := []string{"install", "update", "uninstall"}
	for _, operation := range operations {
		err := enforceInstallDirDestructivePolicy(operation, "/opt", true)
		if err == nil {
			t.Fatalf("expected %s to reject protected system path", operation)
		}
		if !strings.Contains(err.Error(), "protected system path") {
			t.Fatalf("unexpected %s error: %v", operation, err)
		}
	}
}

func TestValidateRuntimeInstallDirStateAllowsMissingImageRefs(t *testing.T) {
	installDir := t.TempDir()
	providersDir := filepath.Join(installDir, "providers", "telemt")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatalf("failed to create providers dir: %v", err)
	}

	envFile := filepath.Join(installDir, ".env")
	composeFile := filepath.Join(installDir, "docker-compose.yml")
	configFile := filepath.Join(providersDir, "telemt.toml")

	if err := os.WriteFile(envFile, []byte("PROVIDER=telemt\n"), 0o600); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}
	if err := os.WriteFile(composeFile, []byte("services:\n  telemt:\n    volumes:\n      - ./providers/telemt/telemt.toml:/etc/telemt.toml:ro\n"), 0o600); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("[general]\n"), 0o600); err != nil {
		t.Fatalf("failed to write telemt config file: %v", err)
	}

	env, err := runtime.LoadEnv(envFile, nil)
	if err != nil {
		t.Fatalf("failed to load env file: %v", err)
	}

	manager := &Manager{logger: fallbackLogger(nil)}
	err = manager.validateRuntimeInstallDirState(installDir, &runtime.RuntimeInstallation{
		Paths: runtime.RuntimePaths{
			InstallDir:  installDir,
			EnvFile:     envFile,
			ComposeFile: composeFile,
		},
		Env: env,
		Provider: runtime.ProviderDescriptor{
			Name:       runtime.ProviderTelemt,
			ConfigPath: configFile,
		},
	})
	if err != nil {
		t.Fatalf("expected runtime preflight to pass without TELEMT_IMAGE/TELEMT_IMAGE_SOURCE, got %v", err)
	}
}

func TestValidateRuntimeInstallDirStateAllowsMissingImageRefsForMTG(t *testing.T) {
	installDir := t.TempDir()
	providersDir := filepath.Join(installDir, "providers", "mtg")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatalf("failed to create providers dir: %v", err)
	}

	envFile := filepath.Join(installDir, ".env")
	composeFile := filepath.Join(installDir, "docker-compose.yml")
	configFile := filepath.Join(providersDir, "mtg.conf")

	if err := os.WriteFile(envFile, []byte("PROVIDER=mtg\n"), 0o600); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}
	if err := os.WriteFile(composeFile, []byte("services:\n  mtg:\n    volumes:\n      - ./providers/mtg/mtg.conf:/config.toml:ro\n"), 0o600); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("secret = \"abcdef\"\n"), 0o600); err != nil {
		t.Fatalf("failed to write mtg config file: %v", err)
	}

	env, err := runtime.LoadEnv(envFile, nil)
	if err != nil {
		t.Fatalf("failed to load env file: %v", err)
	}

	manager := &Manager{logger: fallbackLogger(nil)}
	err = manager.validateRuntimeInstallDirState(installDir, &runtime.RuntimeInstallation{
		Paths: runtime.RuntimePaths{
			InstallDir:  installDir,
			EnvFile:     envFile,
			ComposeFile: composeFile,
		},
		Env: env,
		Provider: runtime.ProviderDescriptor{
			Name:       runtime.ProviderMTG,
			ConfigPath: configFile,
		},
	})
	if err != nil {
		t.Fatalf("expected mtg runtime preflight to pass without MTG_IMAGE/MTG_IMAGE_SOURCE, got %v", err)
	}
}

func TestValidateRuntimeInstallDirStateAllowsProviderFallbackWhenEnvProviderMissing(t *testing.T) {
	installDir := t.TempDir()
	providersDir := filepath.Join(installDir, "providers", "telemt")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatalf("failed to create providers dir: %v", err)
	}

	envFile := filepath.Join(installDir, ".env")
	composeFile := filepath.Join(installDir, "docker-compose.yml")
	configFile := filepath.Join(providersDir, "telemt.toml")

	if err := os.WriteFile(envFile, []byte("API_PORT=9091\n"), 0o600); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}
	if err := os.WriteFile(composeFile, []byte("services:\n  telemt:\n    volumes:\n      - ./providers/telemt/telemt.toml:/etc/telemt.toml:ro\n"), 0o600); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}
	if err := os.WriteFile(configFile, []byte("[general]\n"), 0o600); err != nil {
		t.Fatalf("failed to write telemt config file: %v", err)
	}

	env, err := runtime.LoadEnv(envFile, nil)
	if err != nil {
		t.Fatalf("failed to load env file: %v", err)
	}

	manager := &Manager{logger: fallbackLogger(nil)}
	err = manager.validateRuntimeInstallDirState(installDir, &runtime.RuntimeInstallation{
		Paths: runtime.RuntimePaths{
			InstallDir:  installDir,
			EnvFile:     envFile,
			ComposeFile: composeFile,
		},
		Env: env,
		Provider: runtime.ProviderDescriptor{
			Name:       runtime.ProviderTelemt,
			ConfigPath: configFile,
		},
	})
	if err != nil {
		t.Fatalf("expected runtime preflight to allow provider fallback when PROVIDER is missing, got %v", err)
	}
}

func TestSanitizeEnvOverridesPreservesLowercaseProxyKeys(t *testing.T) {
	sanitized, err := sanitizeEnvOverrides("install", map[string]string{
		"http_proxy":  "http://proxy.internal:3128",
		"INSTALL_DIR": "/opt/mtproxy-installer",
	}, installEnvOverrideAllowlist, envOverrideTrustPolicy{
		AllowTrustBoundaryEnv: true,
		PrivilegedContext:     "",
	})
	if err != nil {
		t.Fatalf("expected install env overrides to pass validation, got %v", err)
	}

	if got := sanitized["http_proxy"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected lowercase proxy key to be preserved, got %q", got)
	}
	if got := sanitized["INSTALL_DIR"]; got != "/opt/mtproxy-installer" {
		t.Fatalf("unexpected INSTALL_DIR value %q", got)
	}
}

func TestSanitizeEnvOverridesRejectsPrivilegedPathOverrides(t *testing.T) {
	_, err := sanitizeEnvOverrides("install", map[string]string{
		"PATH": "/tmp/evil",
	}, installEnvOverrideAllowlist, envOverrideTrustPolicy{})
	if err == nil {
		t.Fatalf("expected PATH override to be rejected for install adapter")
	}

	_, err = sanitizeEnvOverrides("install", map[string]string{
		"DOCKER_CONFIG": "/tmp/user-config",
	}, installEnvOverrideAllowlist, envOverrideTrustPolicy{})
	if err == nil {
		t.Fatalf("expected DOCKER_CONFIG override to be rejected for install adapter")
	}
}

func TestSanitizeEnvOverridesRejectsUnsafeMultilineValue(t *testing.T) {
	_, err := sanitizeEnvOverrides("update", map[string]string{
		"DOCKER_HOST": "tcp://docker.internal:2375\nBAD=1",
		"INSTALL_DIR": "/opt/mtproxy-installer",
	}, updateEnvOverrideAllowlist, envOverrideTrustPolicy{
		AllowTrustBoundaryEnv: true,
		PrivilegedContext:     "",
	})
	if err == nil {
		t.Fatalf("expected multiline env override value to be rejected")
	}
}

func TestParseInstallLifecycleExtractsSensitiveFieldsFromStructuredMarkers(t *testing.T) {
	t.Parallel()

	link := "tg://proxy?server=198.51.100.20&port=443&secret=abcdef"
	summary := ParseInstallLifecycle(execadapter.Result{
		Stdout: "Install dir: /opt/mtproxy-installer\nProvider: telemt\nPublic endpoint: 198.51.100.20:443\nSecret: abcdef\nProxy link:\n" + link + "\nAPI: http://127.0.0.1:9091/v1/health\nConfig: /opt/mtproxy-installer/providers/telemt/telemt.toml\nLogs: docker compose -f /opt/mtproxy-installer/docker-compose.yml logs -f telemt\n[FIX] Telegram voice calls are not guaranteed over MTProto proxy.\n",
	})

	if summary.Provider != "telemt" {
		t.Fatalf("unexpected provider: %q", summary.Provider)
	}
	if summary.InstallDir != "/opt/mtproxy-installer" {
		t.Fatalf("unexpected install dir: %q", summary.InstallDir)
	}
	if summary.Secret != "abcdef" {
		t.Fatalf("unexpected secret: %q", summary.Secret)
	}
	if summary.ProxyLink != link {
		t.Fatalf("unexpected proxy link: %q", summary.ProxyLink)
	}
	if !summary.ProxyLinkPresent {
		t.Fatalf("expected proxy_link_present=true")
	}
	if !summary.SensitiveOutputPresent {
		t.Fatalf("expected sensitive_output_present=true")
	}
	if len(summary.ParseDiagnostics) != 0 {
		t.Fatalf("expected no parse diagnostics, got %v", summary.ParseDiagnostics)
	}
}

func TestParseInstallLifecycleReportsMissingMarkers(t *testing.T) {
	t.Parallel()

	summary := ParseInstallLifecycle(execadapter.Result{
		Stdout: "MTProxy Installer\nStarting telemt...\n",
	})

	if summary.Provider != "" {
		t.Fatalf("expected empty provider when marker is missing, got %q", summary.Provider)
	}
	if len(summary.ParseDiagnostics) == 0 {
		t.Fatalf("expected parse diagnostics for missing markers")
	}
}

func TestResolveRepoRootAcceptsExplicitScriptRoot(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	createScriptSetForRepoRootTest(t, repoRoot)

	resolved, err := resolveRepoRoot(repoRoot, false)
	if err != nil {
		t.Fatalf("expected explicit repo root to be accepted, got %v", err)
	}
	if canonicalPathKey(resolved) != canonicalPathKey(repoRoot) {
		t.Fatalf("unexpected resolved repo root: %q", resolved)
	}
}

func TestResolveRepoRootFromExecutableUsesBinaryRelativePath(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	createScriptSetForRepoRootTest(t, repoRoot)
	executableDir := filepath.Join(repoRoot, "app")
	if err := os.MkdirAll(executableDir, 0o755); err != nil {
		t.Fatalf("failed to create executable directory: %v", err)
	}

	executablePath := filepath.Join(executableDir, "mtproxy")
	if err := os.WriteFile(executablePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("failed to create executable placeholder: %v", err)
	}

	resolved, err := resolveRepoRootFromExecutable(executablePath)
	if err != nil {
		t.Fatalf("expected binary-relative repo root discovery to succeed, got %v", err)
	}
	if canonicalPathKey(resolved) != canonicalPathKey(repoRoot) {
		t.Fatalf("unexpected resolved repo root: %q", resolved)
	}
}

func TestResolveRepoRootFromExecutableFailsWithoutBinaryRelativeScripts(t *testing.T) {
	t.Parallel()

	executablePath := filepath.Join(t.TempDir(), "bin", "mtproxy")
	resolved, err := resolveRepoRootFromExecutable(executablePath)
	if err == nil {
		t.Fatalf("expected binary-relative repo root discovery to fail, got %q", resolved)
	}
	if !strings.Contains(err.Error(), "set ManagerOptions.RepoRoot explicitly") {
		t.Fatalf("expected explicit RepoRoot guidance in error, got %v", err)
	}
}

func TestResolveRepoRootFromExecutableSupportsShareLayout(t *testing.T) {
	t.Parallel()

	prefixDir := t.TempDir()
	repoRoot := filepath.Join(prefixDir, "share", "mtproxy-installer")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("failed to create share layout root: %v", err)
	}
	createScriptSetForRepoRootTest(t, repoRoot)

	binDir := filepath.Join(prefixDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	executablePath := filepath.Join(binDir, "mtproxy")
	if err := os.WriteFile(executablePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("failed to create executable placeholder: %v", err)
	}

	resolved, err := resolveRepoRootFromExecutable(executablePath)
	if err != nil {
		t.Fatalf("expected share-layout repo root discovery to succeed, got %v", err)
	}
	if canonicalPathKey(resolved) != canonicalPathKey(repoRoot) {
		t.Fatalf("unexpected resolved repo root: %q", resolved)
	}
}

func TestValidateEnvRepoRootTrustBoundaryRejectsGroupWritableScripts(t *testing.T) {
	repoRoot := t.TempDir()
	createScriptSetForRepoRootTest(t, repoRoot)

	installScriptPath := filepath.Join(repoRoot, installScriptName)
	if err := os.Chmod(installScriptPath, 0o664); err != nil {
		t.Skipf("chmod is unavailable in this environment: %v", err)
	}

	err := validateEnvRepoRootTrustBoundary(repoRoot, []string{repoRoot})
	if err != nil && strings.Contains(err.Error(), "unable to determine owner") {
		t.Skipf("owner lookup is unavailable in this environment: %v", err)
	}
	if err == nil {
		t.Fatalf("expected trust-boundary validation to reject group-writable script")
	}
	if !strings.Contains(err.Error(), "writable by group/others") {
		t.Fatalf("expected permission failure, got %v", err)
	}
}

func createScriptSetForRepoRootTest(t *testing.T, repoRoot string) {
	t.Helper()

	scriptPaths := []string{
		filepath.Join(repoRoot, installScriptName),
		filepath.Join(repoRoot, updateScriptName),
		filepath.Join(repoRoot, uninstallScriptName),
	}
	for _, scriptPath := range scriptPaths {
		if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\n"), 0o600); err != nil {
			t.Fatalf("failed to create lifecycle script %q: %v", scriptPath, err)
		}
	}
}
