package scripts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/pathutil"
	"mtproxy-installer/app/internal/runtime"
)

const (
	installScriptName   = "install.sh"
	updateScriptName    = "update.sh"
	uninstallScriptName = "uninstall.sh"
)

var trustedBashBinaryCandidates = []string{
	"/usr/bin/bash",
	"/bin/bash",
}

var trustedLifecycleScriptRootCandidates = []string{
	"/opt/mtproxy-installer",
	"/usr/local/share/mtproxy-installer",
	"/usr/local/lib/mtproxy-installer",
	"/usr/local/libexec/mtproxy-installer",
	"/usr/share/mtproxy-installer",
	"/usr/lib/mtproxy-installer",
}

var trustedRepoRootLayoutHints = []string{
	".",
	"../share/mtproxy-installer",
	"../lib/mtproxy-installer",
	"../libexec/mtproxy-installer",
	"../../share/mtproxy-installer",
	"../../lib/mtproxy-installer",
	"../../libexec/mtproxy-installer",
}

var protectedInstallDirTargets = map[string]struct{}{
	"/":      {},
	"/bin":   {},
	"/boot":  {},
	"/dev":   {},
	"/etc":   {},
	"/home":  {},
	"/lib":   {},
	"/lib64": {},
	"/media": {},
	"/mnt":   {},
	"/opt":   {},
	"/proc":  {},
	"/root":  {},
	"/run":   {},
	"/sbin":  {},
	"/srv":   {},
	"/sys":   {},
	"/tmp":   {},
	"/usr":   {},
	"/var":   {},
}

var allowedInstallDirRoots = []string{
	filepath.Dir(runtime.DefaultInstallDir),
}

var runtimeComposeMarkers = map[runtime.Provider]string{
	runtime.ProviderTelemt: "./providers/telemt/telemt.toml",
	runtime.ProviderMTG:    "./providers/mtg/mtg.conf",
}

var privilegedExecutionEnvOptInAllowlist = []string{
	"HOME",
	"XDG_RUNTIME_DIR",
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"http_proxy",
	"https_proxy",
	"no_proxy",
	"DOCKER_HOST",
	"DOCKER_CONTEXT",
	"DOCKER_API_VERSION",
	"DOCKER_TLS",
	"DOCKER_CERT_PATH",
	"DOCKER_TLS_VERIFY",
	"COMPOSE_PROJECT_NAME",
	"COMPOSE_PROFILES",
	"COMPOSE_PARALLEL_LIMIT",
}

var privilegedExecutionEnvOptInPrefixAllowlist = []string{
	"DOCKER_TLS_",
}

var trustBoundaryEnvOverrideKeyAllowlist = map[string]struct{}{
	"HOME":             {},
	"XDG_RUNTIME_DIR":  {},
	"HTTP_PROXY":       {},
	"HTTPS_PROXY":      {},
	"NO_PROXY":         {},
	"DOCKER_HOST":      {},
	"DOCKER_CERT_PATH": {},
}

var trustBoundaryEnvOverridePrefixAllowlist = []string{
	"DOCKER_",
	"COMPOSE_",
}

var installEnvOverrideAllowlist = mergeAllowedEnvKeys(
	[]string{
		"PROVIDER",
		"PORT",
		"INSTALL_DIR",
		"API_PORT",
		"TLS_DOMAIN",
		"PUBLIC_IP",
		"SECRET",
		"TELEMT_IMAGE",
		"TELEMT_IMAGE_SOURCE",
		"MTG_IMAGE",
		"MTG_IMAGE_SOURCE",
		"RUST_LOG",
		"MTG_DEBUG",
		"PROXY_USER",
	},
	privilegedExecutionEnvOptInAllowlist,
)

var updateEnvOverrideAllowlist = mergeAllowedEnvKeys(
	[]string{
		"INSTALL_DIR",
	},
	privilegedExecutionEnvOptInAllowlist,
)

var uninstallEnvOverrideAllowlist = mergeAllowedEnvKeys(
	[]string{
		"INSTALL_DIR",
		"KEEP_DATA",
	},
	privilegedExecutionEnvOptInAllowlist,
)

var (
	installValueUnsafeCharsPattern  = regexp.MustCompile(`[\r\n"'` + "`" + `]`)
	privilegedEnvValueUnsafePattern = regexp.MustCompile(`[\r\n]`)
	tlsDomainPattern                = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	secretValuePattern              = regexp.MustCompile(`^[A-Za-z0-9._~:+-]{8,256}$`)
	proxyUserValuePattern           = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,64}$`)
	imageReferencePattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@:-]{0,254}$`)
	installProxyLinkPattern         = regexp.MustCompile(`(?i)^(tg://proxy\?[^\s]+|https://t\.me/proxy\?\S+)$`)
)

type ManagerOptions struct {
	Runner          *execadapter.Runner
	Logger          *slog.Logger
	RepoRoot        string
	RepoRootFromEnv bool
	BashPath        string
}

type Manager struct {
	runner                   *execadapter.Runner
	logger                   *slog.Logger
	repoRoot                 string
	bashPath                 string
	privilegedExecutionScope string
}

type InstallOptions struct {
	Provider                  runtime.Provider
	Port                      int
	InstallDir                string
	AllowNonDefaultInstallDir bool
	APIPort                   int
	TLSDomain                 string
	PublicIP                  string
	Secret                    string
	ProxyUser                 string
	TelemtImage               string
	TelemtImageSource         string
	MTGImage                  string
	MTGImageSource            string
	ExtraEnv                  map[string]string
	AllowTrustBoundaryEnv     bool
}

type envOverrideTrustPolicy struct {
	AllowTrustBoundaryEnv bool
	PrivilegedContext     string
}

type InstallLifecycleSummary struct {
	Provider               string
	InstallDir             string
	PublicEndpoint         string
	APIEndpoint            string
	ConfigPath             string
	LogsHint               string
	Secret                 string
	ProxyLink              string
	ProxyLinkPresent       bool
	SensitiveOutputPresent bool
	OperatorHints          []string
	ParseDiagnostics       []string
}

func NewManager(options ManagerOptions) (*Manager, error) {
	logger := fallbackLogger(options.Logger)

	repoRoot, err := resolveRepoRoot(options.RepoRoot, options.RepoRootFromEnv)
	if err != nil {
		return nil, err
	}
	bashPath, err := resolveTrustedBinaryPath("bash", options.BashPath, trustedBashBinaryCandidates)
	if err != nil {
		return nil, err
	}

	runner := options.Runner
	if runner == nil {
		runner = execadapter.NewRunner(logger)
	}

	privilegedScope := detectPrivilegedExecutionContext()

	logger.Debug(
		"script adapter manager initialized",
		"repo_root", repoRoot,
		"repo_root_from_env", options.RepoRootFromEnv,
		"bash_path", bashPath,
		"privileged_execution_scope", normalizeLogValue(privilegedScope, "none"),
	)

	return &Manager{
		runner:                   runner,
		logger:                   logger,
		repoRoot:                 repoRoot,
		bashPath:                 bashPath,
		privilegedExecutionScope: privilegedScope,
	}, nil
}

func (m *Manager) Install(ctx context.Context, options InstallOptions) (execadapter.Result, error) {
	provider, err := normalizeProvider(options.Provider)
	if err != nil {
		return execadapter.Result{}, err
	}

	if options.Port < 1 || options.Port > 65535 {
		return execadapter.Result{}, fmt.Errorf("invalid install port: %d", options.Port)
	}

	installDir, err := m.preflightInstallTarget(options.InstallDir)
	if err != nil {
		return execadapter.Result{}, err
	}
	if err := enforceInstallDirDestructivePolicy("install", installDir, options.AllowNonDefaultInstallDir); err != nil {
		return execadapter.Result{}, err
	}

	scriptPath, err := m.resolveScriptPath(installScriptName)
	if err != nil {
		return execadapter.Result{}, err
	}

	positionalArgs := []string{
		provider,
		strconv.Itoa(options.Port),
	}

	envOverrides := copyEnv(options.ExtraEnv)
	envOverrides["PROVIDER"] = provider
	envOverrides["PORT"] = strconv.Itoa(options.Port)
	envOverrides["INSTALL_DIR"] = installDir
	putIfNotEmpty(envOverrides, "TLS_DOMAIN", options.TLSDomain)
	putIfNotEmpty(envOverrides, "PUBLIC_IP", options.PublicIP)
	putIfNotEmpty(envOverrides, "SECRET", options.Secret)
	putIfNotEmpty(envOverrides, "PROXY_USER", options.ProxyUser)
	putIfNotEmpty(envOverrides, "TELEMT_IMAGE", options.TelemtImage)
	putIfNotEmpty(envOverrides, "TELEMT_IMAGE_SOURCE", options.TelemtImageSource)
	putIfNotEmpty(envOverrides, "MTG_IMAGE", options.MTGImage)
	putIfNotEmpty(envOverrides, "MTG_IMAGE_SOURCE", options.MTGImageSource)
	if options.APIPort > 0 {
		envOverrides["API_PORT"] = strconv.Itoa(options.APIPort)
	}
	if err := sanitizeInstallEnvValueMap(envOverrides); err != nil {
		return execadapter.Result{}, err
	}
	envOverrides, err = sanitizeEnvOverrides(
		"install",
		envOverrides,
		installEnvOverrideAllowlist,
		envOverrideTrustPolicy{
			AllowTrustBoundaryEnv: options.AllowTrustBoundaryEnv,
			PrivilegedContext:     m.privilegedExecutionScope,
		},
	)
	if err != nil {
		return execadapter.Result{}, err
	}
	installDir, err = m.recheckInstallTargetAtExecution(installDir)
	if err != nil {
		return execadapter.Result{}, err
	}
	if err := enforceInstallDirDestructivePolicy("install", installDir, options.AllowNonDefaultInstallDir); err != nil {
		return execadapter.Result{}, err
	}
	envOverrides["INSTALL_DIR"] = installDir

	m.logger.Debug(
		"install adapter request assembled",
		"script_path", scriptPath,
		"provider", provider,
		"positional_args", execadapter.RedactArgs(positionalArgs),
		"install_dir", installDir,
		"allow_non_default_install_dir", options.AllowNonDefaultInstallDir,
		"working_dir", m.repoRoot,
		"env_override_keys", sortedKeys(envOverrides),
		"env_overrides", execadapter.RedactEnvSnapshot(envOverrides),
	)
	m.logger.Info(
		"install adapter start",
		"script_path", scriptPath,
		"provider", provider,
		"install_dir", installDir,
	)

	requestArgs := append([]string{scriptPath}, positionalArgs...)
	result, runErr := m.runner.Run(ctx, execadapter.Request{
		Command:          m.bashPath,
		Args:             requestArgs,
		WorkingDir:       m.repoRoot,
		EnvOverrides:     envOverrides,
		InheritParentEnv: false,
		AllowedEnvKeys:   sortedKeys(envOverrides),
		UseSafePath:      true,
	})
	lifecycleSummary := ParseInstallLifecycle(result)
	m.logger.Debug(
		"install adapter parsed lifecycle summary",
		"script_path", scriptPath,
		"provider", normalizeLogValue(lifecycleSummary.Provider, provider),
		"install_dir", normalizeLogValue(lifecycleSummary.InstallDir, installDir),
		"public_endpoint", normalizeLogValue(lifecycleSummary.PublicEndpoint, "n/a"),
		"api_endpoint", normalizeLogValue(lifecycleSummary.APIEndpoint, "n/a"),
		"config_path", normalizeLogValue(lifecycleSummary.ConfigPath, "n/a"),
		"proxy_link_present", lifecycleSummary.ProxyLinkPresent,
		"sensitive_output_present", lifecycleSummary.SensitiveOutputPresent,
		"parse_diagnostics", lifecycleSummary.ParseDiagnostics,
	)
	if runErr != nil {
		m.logger.Error(
			"install adapter failed",
			"script_path", scriptPath,
			"provider", normalizeLogValue(lifecycleSummary.Provider, provider),
			"install_dir", normalizeLogValue(lifecycleSummary.InstallDir, installDir),
			"public_endpoint", normalizeLogValue(lifecycleSummary.PublicEndpoint, "n/a"),
			"proxy_link_present", lifecycleSummary.ProxyLinkPresent,
			"sensitive_output_present", lifecycleSummary.SensitiveOutputPresent,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
			"stderr_summary", result.StderrSummary,
			"error", execadapter.RedactText(runErr.Error()),
		)
		return result, runErr
	}

	m.logger.Info(
		"install adapter finish",
		"script_path", scriptPath,
		"provider", normalizeLogValue(lifecycleSummary.Provider, provider),
		"install_dir", normalizeLogValue(lifecycleSummary.InstallDir, installDir),
		"public_endpoint", normalizeLogValue(lifecycleSummary.PublicEndpoint, "n/a"),
		"api_endpoint", normalizeLogValue(lifecycleSummary.APIEndpoint, "n/a"),
		"config_path", normalizeLogValue(lifecycleSummary.ConfigPath, "n/a"),
		"proxy_link_present", lifecycleSummary.ProxyLinkPresent,
		"sensitive_output_present", lifecycleSummary.SensitiveOutputPresent,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
		"stderr_summary", result.StderrSummary,
	)

	return result, nil
}

func (m *Manager) resolveScriptPath(scriptName string) (string, error) {
	return resolveScriptPathFromRoot(m.repoRoot, scriptName)
}

func (m *Manager) resolveUninstallScriptPath() (string, error) {
	if strings.TrimSpace(m.privilegedExecutionScope) == "" {
		return m.resolveScriptPath(uninstallScriptName)
	}

	trustedRoot, err := resolvePrivilegedUninstallScriptRoot()
	if err != nil {
		return "", err
	}

	m.logger.Debug(
		"uninstall adapter resolved privileged script root",
		"script_root", trustedRoot,
		"privileged_execution_scope", m.privilegedExecutionScope,
	)

	return resolveScriptPathFromRoot(trustedRoot, uninstallScriptName)
}

func resolveScriptPathFromRoot(scriptRoot string, scriptName string) (string, error) {
	candidate := filepath.Clean(filepath.Join(scriptRoot, scriptName))
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", fmt.Errorf("unable to resolve %s: %w", scriptName, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("script path must not be a symlink: %s", candidate)
	}
	if info.IsDir() {
		return "", fmt.Errorf("script path points to directory: %s", candidate)
	}
	if err := validatePathChainNoSymlinks(candidate); err != nil {
		return "", fmt.Errorf("script path chain is unsafe for %s: %w", scriptName, err)
	}
	return candidate, nil
}

func resolvePrivilegedUninstallScriptRoot() (string, error) {
	candidates := make([]string, 0, 16)
	if executablePath, err := os.Executable(); err == nil {
		if executableCandidates, collectErr := collectRepoRootCandidatesFromExecutable(executablePath); collectErr == nil {
			candidates = append(candidates, executableCandidates...)
		}
	}
	for _, candidate := range trustedLifecycleScriptRootCandidates {
		candidates = append(candidates, filepath.Clean(filepath.FromSlash(candidate)))
	}

	validationFailures := make([]string, 0, len(candidates))
	for _, candidate := range deduplicateCandidatePaths(candidates) {
		resolved, err := validateRepoRootCandidate(candidate, "privileged uninstall script root", true)
		if err != nil {
			validationFailures = append(validationFailures, err.Error())
			continue
		}
		if err := validateLifecycleScriptRootTrust(resolved); err != nil {
			validationFailures = append(validationFailures, err.Error())
			continue
		}
		return resolved, nil
	}

	if len(validationFailures) == 0 {
		return "", errors.New("unable to resolve trusted script root for privileged uninstall")
	}
	return "", fmt.Errorf(
		"unable to resolve trusted script root for privileged uninstall: %s",
		strings.Join(validationFailures, " | "),
	)
}

func validateLifecycleScriptRootTrust(root string) error {
	checkedPaths := []string{
		root,
		filepath.Join(root, installScriptName),
		filepath.Join(root, updateScriptName),
		filepath.Join(root, uninstallScriptName),
	}

	for _, path := range checkedPaths {
		if err := ensurePathOwnershipTrusted(path); err != nil {
			return fmt.Errorf("trusted script root ownership check failed: %w", err)
		}
		if err := ensurePathPermissionsTrusted(path); err != nil {
			return fmt.Errorf("trusted script root permission check failed: %w", err)
		}
	}

	return nil
}

func enforceInstallDirDestructivePolicy(operation string, installDir string, allowNonDefaultInstallDir bool) error {
	resolvedInstallDir, err := resolveInstallDirPath(installDir)
	if err != nil {
		return fmt.Errorf("%s adapter INSTALL_DIR policy check failed: %w", operation, err)
	}
	if err := validateInstallDirPathSafety(resolvedInstallDir); err != nil {
		return fmt.Errorf("%s adapter INSTALL_DIR policy check failed: %w", operation, err)
	}
	defaultInstallDir, err := resolveInstallDirPath(runtime.DefaultInstallDir)
	if err != nil {
		return fmt.Errorf("%s adapter INSTALL_DIR policy check failed: unable to resolve default install dir: %w", operation, err)
	}

	if canonicalPathKey(resolvedInstallDir) == canonicalPathKey(defaultInstallDir) {
		return nil
	}

	resolvedAllowedRoots, err := resolveAllowedInstallDirRoots()
	if err != nil {
		return fmt.Errorf("%s adapter INSTALL_DIR policy check failed: %w", operation, err)
	}
	if !isWithinAllowedInstallDirRoots(resolvedInstallDir, resolvedAllowedRoots) {
		return fmt.Errorf(
			"%s adapter refuses INSTALL_DIR %q: path must be under allowed install roots [%s]",
			operation,
			resolvedInstallDir,
			strings.Join(resolvedAllowedRoots, ", "),
		)
	}

	if allowNonDefaultInstallDir {
		return nil
	}

	return fmt.Errorf(
		"%s adapter requires explicit AllowNonDefaultInstallDir=true for non-default INSTALL_DIR %q",
		operation,
		resolvedInstallDir,
	)
}

func resolveAllowedInstallDirRoots() ([]string, error) {
	resolved := make([]string, 0, len(allowedInstallDirRoots))
	seen := map[string]struct{}{}

	for _, candidate := range allowedInstallDirRoots {
		root, err := resolveInstallDirPath(candidate)
		if err != nil {
			return nil, fmt.Errorf("unable to resolve allowed install root %q: %w", candidate, err)
		}
		key := canonicalPathKey(root)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		resolved = append(resolved, root)
	}

	sort.Strings(resolved)
	if len(resolved) == 0 {
		return nil, errors.New("no allowed install roots configured")
	}
	return resolved, nil
}

func isWithinAllowedInstallDirRoots(installDir string, roots []string) bool {
	for _, root := range roots {
		if canonicalPathKey(installDir) == canonicalPathKey(root) {
			return true
		}
		if isPathWithin(root, installDir) {
			return true
		}
	}
	return false
}

func (m *Manager) preflightInstallTarget(installDir string) (string, error) {
	resolvedInstallDir, err := resolveInstallDirPath(installDir)
	if err != nil {
		return "", err
	}
	if err := enforceInstallDirPathSafety(resolvedInstallDir, true); err != nil {
		return "", err
	}

	info, statErr := os.Stat(resolvedInstallDir)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			parentDir := filepath.Dir(resolvedInstallDir)
			if err := validatePathChainNoSymlinks(parentDir); err != nil {
				return "", fmt.Errorf("invalid INSTALL_DIR %q: parent path is unsafe: %w", resolvedInstallDir, err)
			}
			if err := requireRuntimeDirectory(parentDir, "INSTALL_DIR parent directory"); err != nil {
				return "", fmt.Errorf("invalid INSTALL_DIR %q: %w", resolvedInstallDir, err)
			}
			return resolvedInstallDir, nil
		}
		return "", fmt.Errorf("unable to inspect INSTALL_DIR %q: %w", resolvedInstallDir, statErr)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("invalid INSTALL_DIR %q: path is not a directory", resolvedInstallDir)
	}

	entries, readErr := os.ReadDir(resolvedInstallDir)
	if readErr != nil {
		return "", fmt.Errorf("unable to read INSTALL_DIR %q: %w", resolvedInstallDir, readErr)
	}
	if len(entries) == 0 {
		return resolvedInstallDir, nil
	}

	_, runtimeErr := m.preflightRuntimeInstallDir(resolvedInstallDir)
	if runtimeErr != nil {
		return "", fmt.Errorf("refusing to reuse INSTALL_DIR %q: non-empty path is not a valid mtproxy runtime: %w", resolvedInstallDir, runtimeErr)
	}

	return resolvedInstallDir, nil
}

func (m *Manager) preflightRuntimeInstallDir(installDir string) (*runtime.RuntimeInstallation, error) {
	resolvedInstallDir, err := resolveInstallDirPath(installDir)
	if err != nil {
		return nil, err
	}
	if err := enforceInstallDirPathSafety(resolvedInstallDir, false); err != nil {
		return nil, err
	}

	runtimeState, err := runtime.Load(runtime.LoadOptions{
		InstallDir: resolvedInstallDir,
		Logger:     m.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", resolvedInstallDir, err)
	}

	if err := m.validateRuntimeInstallDirState(resolvedInstallDir, runtimeState); err != nil {
		return nil, err
	}

	return runtimeState, nil
}

func (m *Manager) validateRuntimeInstallDirState(installDir string, runtimeState *runtime.RuntimeInstallation) error {
	if runtimeState == nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: runtime state is empty", installDir)
	}
	if err := ensurePathNotSymlink(installDir, "INSTALL_DIR"); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	if err := validatePathChainNoSymlinks(installDir); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}

	if canonicalPathKey(installDir) != canonicalPathKey(runtimeState.Paths.InstallDir) {
		return fmt.Errorf(
			"INSTALL_DIR preflight failed for %q: resolved runtime path mismatch (%q)",
			installDir,
			runtimeState.Paths.InstallDir,
		)
	}

	if !isPathWithin(installDir, runtimeState.Paths.EnvFile) {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: env file points outside runtime (%q)", installDir, runtimeState.Paths.EnvFile)
	}
	if !isPathWithin(installDir, runtimeState.Paths.ComposeFile) {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: compose file points outside runtime (%q)", installDir, runtimeState.Paths.ComposeFile)
	}
	if !isPathWithin(installDir, runtimeState.Provider.ConfigPath) {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: provider config points outside runtime (%q)", installDir, runtimeState.Provider.ConfigPath)
	}
	if err := ensurePathNotSymlink(runtimeState.Paths.EnvFile, "runtime env file"); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	if err := validatePathChainNoSymlinks(runtimeState.Paths.EnvFile); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	if err := ensurePathNotSymlink(runtimeState.Paths.ComposeFile, "runtime compose file"); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	if err := validatePathChainNoSymlinks(runtimeState.Paths.ComposeFile); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	if err := ensurePathNotSymlink(runtimeState.Provider.ConfigPath, "runtime provider config"); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	if err := validatePathChainNoSymlinks(runtimeState.Provider.ConfigPath); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}

	providersDir := filepath.Join(installDir, "providers")
	if err := requireRuntimeDirectory(providersDir, "runtime providers directory"); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}
	providerDir := filepath.Join(providersDir, string(runtimeState.Provider.Name))
	if err := requireRuntimeDirectory(providerDir, "runtime provider directory"); err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: %w", installDir, err)
	}

	envProvider := runtimeState.Env.ProviderValue()
	if envProvider != "" && !strings.EqualFold(envProvider, string(runtimeState.Provider.Name)) {
		return fmt.Errorf(
			"INSTALL_DIR preflight failed for %q: provider mismatch env=%q runtime=%q",
			installDir,
			envProvider,
			runtimeState.Provider.Name,
		)
	}

	if configuredInstallDir, ok := runtimeState.Env.Value(runtime.InstallDirEnvKey); ok && strings.TrimSpace(configuredInstallDir) != "" {
		resolvedConfiguredDir, resolveErr := resolveInstallDirPath(configuredInstallDir)
		if resolveErr != nil {
			return fmt.Errorf(
				"INSTALL_DIR preflight failed for %q: runtime env INSTALL_DIR is invalid (%q): %w",
				installDir,
				configuredInstallDir,
				resolveErr,
			)
		}
		if canonicalPathKey(resolvedConfiguredDir) != canonicalPathKey(installDir) {
			return fmt.Errorf(
				"INSTALL_DIR preflight failed for %q: runtime env INSTALL_DIR points to %q",
				installDir,
				configuredInstallDir,
			)
		}
	}

	// Keep compatibility with existing runtime contracts: update.sh/uninstall.sh
	// resolve provider image refs with shell-side fallbacks, so preflight must not
	// require TELEMT_IMAGE{_SOURCE} or MTG_IMAGE{_SOURCE} to be present.

	marker, ok := runtimeComposeMarkers[runtimeState.Provider.Name]
	if !ok {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: no compose marker for provider %q", installDir, runtimeState.Provider.Name)
	}

	composeBody, err := os.ReadFile(runtimeState.Paths.ComposeFile)
	if err != nil {
		return fmt.Errorf("INSTALL_DIR preflight failed for %q: unable to read compose file: %w", installDir, err)
	}
	composeText := strings.ToLower(string(composeBody))
	if !strings.Contains(composeText, strings.ToLower(marker)) {
		return fmt.Errorf(
			"INSTALL_DIR preflight failed for %q: compose file does not contain provider marker %q",
			installDir,
			marker,
		)
	}

	m.logger.Debug(
		"runtime INSTALL_DIR preflight passed",
		"install_dir", installDir,
		"provider", runtimeState.Provider.Name,
		"compose_file", runtimeState.Paths.ComposeFile,
		"env_file", runtimeState.Paths.EnvFile,
	)

	return nil
}

func (m *Manager) recheckInstallTargetAtExecution(expectedInstallDir string) (string, error) {
	recheckedInstallDir, err := m.preflightInstallTarget(expectedInstallDir)
	if err != nil {
		return "", fmt.Errorf("INSTALL_DIR execution recheck failed for %q: %w", expectedInstallDir, err)
	}
	if canonicalPathKey(recheckedInstallDir) != canonicalPathKey(expectedInstallDir) {
		return "", fmt.Errorf(
			"INSTALL_DIR execution recheck failed for %q: path changed to %q",
			expectedInstallDir,
			recheckedInstallDir,
		)
	}
	return recheckedInstallDir, nil
}

func (m *Manager) recheckRuntimeStateAtExecution(snapshot *runtime.RuntimeInstallation) (*runtime.RuntimeInstallation, error) {
	if snapshot == nil {
		return nil, errors.New("runtime execution recheck requires runtime snapshot")
	}

	rechecked, err := m.preflightRuntimeInstallDir(snapshot.Paths.InstallDir)
	if err != nil {
		return nil, fmt.Errorf("runtime execution recheck failed for %q: %w", snapshot.Paths.InstallDir, err)
	}

	if canonicalPathKey(snapshot.Paths.InstallDir) != canonicalPathKey(rechecked.Paths.InstallDir) {
		return nil, fmt.Errorf(
			"runtime execution recheck failed for %q: install dir changed to %q",
			snapshot.Paths.InstallDir,
			rechecked.Paths.InstallDir,
		)
	}
	if canonicalPathKey(snapshot.Paths.EnvFile) != canonicalPathKey(rechecked.Paths.EnvFile) {
		return nil, fmt.Errorf(
			"runtime execution recheck failed for %q: env file changed to %q",
			snapshot.Paths.InstallDir,
			rechecked.Paths.EnvFile,
		)
	}
	if canonicalPathKey(snapshot.Paths.ComposeFile) != canonicalPathKey(rechecked.Paths.ComposeFile) {
		return nil, fmt.Errorf(
			"runtime execution recheck failed for %q: compose file changed to %q",
			snapshot.Paths.InstallDir,
			rechecked.Paths.ComposeFile,
		)
	}
	if canonicalPathKey(snapshot.Provider.ConfigPath) != canonicalPathKey(rechecked.Provider.ConfigPath) {
		return nil, fmt.Errorf(
			"runtime execution recheck failed for %q: provider config changed to %q",
			snapshot.Paths.InstallDir,
			rechecked.Provider.ConfigPath,
		)
	}
	if !strings.EqualFold(string(snapshot.Provider.Name), string(rechecked.Provider.Name)) {
		return nil, fmt.Errorf(
			"runtime execution recheck failed for %q: provider changed from %q to %q",
			snapshot.Paths.InstallDir,
			snapshot.Provider.Name,
			rechecked.Provider.Name,
		)
	}

	return rechecked, nil
}

func resolveRepoRoot(explicitRepoRoot string, repoRootFromEnv bool) (string, error) {
	autoCandidates := collectAutoRepoRootCandidates()
	autoRoots, autoFailures := resolveRepoRootCandidates(autoCandidates)

	repoRoot := strings.TrimSpace(explicitRepoRoot)
	if repoRoot != "" {
		resolved, err := validateRepoRootCandidate(repoRoot, "explicit manager repo root", false)
		if err != nil {
			return "", err
		}
		if repoRootFromEnv {
			if err := validateEnvRepoRootTrustBoundary(resolved, autoRoots); err != nil {
				return "", err
			}
		}
		return resolved, nil
	}

	if len(autoRoots) > 0 {
		return autoRoots[0], nil
	}

	if len(autoFailures) == 0 {
		return "", errors.New("unable to resolve repository root from trusted runtime locations")
	}
	return "", fmt.Errorf(
		"unable to resolve repository root from trusted runtime locations; set ManagerOptions.RepoRoot explicitly: %s",
		strings.Join(autoFailures, " | "),
	)
}

func resolveRepoRootFromExecutable(executablePath string) (string, error) {
	candidates, err := collectRepoRootCandidatesFromExecutable(executablePath)
	if err != nil {
		return "", err
	}

	roots, failures := resolveRepoRootCandidates(candidates)
	if len(roots) > 0 {
		return roots[0], nil
	}
	if len(failures) == 0 {
		return "", fmt.Errorf(
			"unable to resolve repository root from executable path %q; set ManagerOptions.RepoRoot explicitly",
			strings.TrimSpace(executablePath),
		)
	}
	return "", fmt.Errorf(
		"unable to resolve repository root from executable path %q; set ManagerOptions.RepoRoot explicitly: %s",
		strings.TrimSpace(executablePath),
		strings.Join(failures, " | "),
	)
}

func collectAutoRepoRootCandidates() []string {
	candidates := make([]string, 0, 32)

	if cwd, err := os.Getwd(); err == nil {
		candidates = appendRepoRootCandidatesFromSeed(candidates, cwd)
	}

	if executablePath, err := os.Executable(); err == nil {
		if executableCandidates, collectErr := collectRepoRootCandidatesFromExecutable(executablePath); collectErr == nil {
			candidates = append(candidates, executableCandidates...)
		}
	}

	if _, sourceFile, _, ok := goruntime.Caller(0); ok {
		candidates = appendRepoRootCandidatesFromSeed(candidates, sourceFile)
	}

	return deduplicateCandidatePaths(candidates)
}

func collectRepoRootCandidatesFromExecutable(executablePath string) ([]string, error) {
	trimmedPath := strings.TrimSpace(executablePath)
	if trimmedPath == "" {
		return nil, errors.New("unable to resolve repository root: executable path is empty")
	}

	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve executable path %q: %w", trimmedPath, err)
	}
	absolutePath = filepath.Clean(absolutePath)
	if err := validatePathChainNoSymlinks(absolutePath); err != nil {
		return nil, fmt.Errorf("unable to trust executable path %q: %w", absolutePath, err)
	}

	executableDir := filepath.Dir(absolutePath)
	candidates := appendRepoRootCandidatesFromSeed(make([]string, 0, 16), executableDir)
	for _, hint := range trustedRepoRootLayoutHints {
		relative := strings.TrimSpace(hint)
		if relative == "" {
			continue
		}
		candidates = append(candidates, filepath.Clean(filepath.Join(executableDir, relative)))
	}
	return deduplicateCandidatePaths(candidates), nil
}

func appendRepoRootCandidatesFromSeed(target []string, seedPath string) []string {
	trimmed := strings.TrimSpace(seedPath)
	if trimmed == "" {
		return target
	}

	absolutePath, err := filepath.Abs(trimmed)
	if err != nil {
		return target
	}
	absolutePath = filepath.Clean(absolutePath)
	if info, statErr := os.Stat(absolutePath); statErr == nil && !info.IsDir() {
		absolutePath = filepath.Dir(absolutePath)
	}

	current := absolutePath
	for {
		target = append(target, current)
		parent := filepath.Dir(current)
		if canonicalPathKey(parent) == canonicalPathKey(current) {
			break
		}
		current = parent
	}

	return target
}

func deduplicateCandidatePaths(candidates []string) []string {
	visited := map[string]struct{}{}
	deduplicated := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		absolutePath, err := filepath.Abs(trimmed)
		if err != nil {
			continue
		}
		normalized := filepath.Clean(absolutePath)
		key := canonicalPathKey(normalized)
		if key == "" {
			continue
		}
		if _, exists := visited[key]; exists {
			continue
		}
		visited[key] = struct{}{}
		deduplicated = append(deduplicated, normalized)
	}
	return deduplicated
}

func resolveRepoRootCandidates(candidates []string) ([]string, []string) {
	roots := make([]string, 0, len(candidates))
	failures := make([]string, 0, len(candidates))
	for _, candidate := range deduplicateCandidatePaths(candidates) {
		resolved, err := validateRepoRootCandidate(candidate, "auto-discovered script root", false)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		roots = append(roots, resolved)
	}
	return deduplicateCandidatePaths(roots), failures
}

func validateEnvRepoRootTrustBoundary(repoRoot string, discoveredRoots []string) error {
	absoluteRoot := filepath.Clean(repoRoot)
	if !filepath.IsAbs(absoluteRoot) {
		return fmt.Errorf("MTPROXY_SCRIPTS_ROOT must be an absolute path: %q", repoRoot)
	}

	allowedRoots := deduplicateCandidatePaths(append([]string{}, discoveredRoots...))
	for _, candidate := range trustedLifecycleScriptRootCandidates {
		allowedRoots = append(allowedRoots, filepath.Clean(filepath.FromSlash(candidate)))
	}
	allowedRoots = deduplicateCandidatePaths(allowedRoots)

	allowed := false
	for _, trustedRoot := range allowedRoots {
		if canonicalPathKey(absoluteRoot) == canonicalPathKey(trustedRoot) || isPathWithin(trustedRoot, absoluteRoot) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf(
			"MTPROXY_SCRIPTS_ROOT %q is outside trusted script directories [%s]",
			absoluteRoot,
			strings.Join(allowedRoots, ", "),
		)
	}

	checkedPaths := []string{
		absoluteRoot,
		filepath.Join(absoluteRoot, installScriptName),
		filepath.Join(absoluteRoot, updateScriptName),
		filepath.Join(absoluteRoot, uninstallScriptName),
	}
	for _, path := range checkedPaths {
		if err := ensurePathOwnershipTrusted(path); err != nil {
			return fmt.Errorf("MTPROXY_SCRIPTS_ROOT trust check failed: %w", err)
		}
		if err := ensurePathPermissionsTrusted(path); err != nil {
			return fmt.Errorf("MTPROXY_SCRIPTS_ROOT trust check failed: %w", err)
		}
	}

	return nil
}

func ensurePathOwnershipTrusted(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("unable to inspect ownership of %q: %w", path, err)
	}

	ownerUID, ok := readPathOwnerUID(info)
	if !ok {
		return fmt.Errorf("unable to determine owner for %q", path)
	}
	currentUID, currentOK := currentUserUID()
	if !currentOK {
		return fmt.Errorf("unable to determine current process owner for %q", path)
	}
	if ownerUID != 0 && ownerUID != currentUID {
		return fmt.Errorf("owner of %q is untrusted uid=%d", path, ownerUID)
	}
	return nil
}

func ensurePathPermissionsTrusted(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("unable to inspect permissions of %q: %w", path, err)
	}

	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%q is writable by group/others (mode=%#o)", path, info.Mode().Perm())
	}
	return nil
}

func readPathOwnerUID(info os.FileInfo) (uint64, bool) {
	if info == nil || info.Sys() == nil {
		return 0, false
	}

	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return 0, false
	}

	uidField := value.FieldByName("Uid")
	if !uidField.IsValid() {
		return 0, false
	}

	switch uidField.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uidField.Uint(), true
	default:
		return 0, false
	}
}

func currentUserUID() (uint64, bool) {
	current, err := user.Current()
	if err != nil {
		return 0, false
	}
	uid := strings.TrimSpace(current.Uid)
	if uid == "" {
		return 0, false
	}
	numericUID, parseErr := strconv.ParseUint(uid, 10, 64)
	if parseErr != nil {
		return 0, false
	}
	return numericUID, true
}

func validateRepoRootCandidate(candidatePath string, source string, strictPermissions bool) (string, error) {
	trimmed := strings.TrimSpace(candidatePath)
	if trimmed == "" {
		return "", fmt.Errorf("%s is empty", source)
	}

	absolutePath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("%s %q cannot be resolved: %w", source, candidatePath, err)
	}
	absolutePath = filepath.Clean(absolutePath)

	if err := requireRuntimeDirectory(absolutePath, "repository root"); err != nil {
		return "", fmt.Errorf("%s %q is invalid: %w", source, absolutePath, err)
	}
	if err := validatePathChainNoSymlinks(absolutePath); err != nil {
		return "", fmt.Errorf("%s %q is unsafe: %w", source, absolutePath, err)
	}
	if !hasScriptSet(absolutePath) {
		return "", fmt.Errorf("%s %q does not contain required lifecycle scripts", source, absolutePath)
	}
	if strictPermissions {
		if err := ensurePathPermissionsTrusted(absolutePath); err != nil {
			return "", fmt.Errorf("%s %q failed strict permissions check: %w", source, absolutePath, err)
		}
	}

	return absolutePath, nil
}

func hasScriptSet(path string) bool {
	required := []string{
		installScriptName,
		updateScriptName,
		uninstallScriptName,
	}

	for _, scriptName := range required {
		candidate := filepath.Join(path, scriptName)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			return false
		}
	}

	return true
}

func resolveTrustedBinaryPath(binaryName string, explicitPath string, candidates []string) (string, error) {
	trimmedExplicitPath := strings.TrimSpace(explicitPath)
	if trimmedExplicitPath != "" {
		return validateTrustedBinaryPath(binaryName, trimmedExplicitPath)
	}

	for _, candidate := range candidates {
		validated, err := validateTrustedBinaryPath(binaryName, candidate)
		if err == nil {
			return validated, nil
		}
	}

	return "", fmt.Errorf("unable to resolve trusted %s binary path", binaryName)
}

func validateTrustedBinaryPath(binaryName string, candidate string) (string, error) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", fmt.Errorf("%s binary path is required", binaryName)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%s binary path must be absolute: %q", binaryName, trimmed)
	}

	resolved := filepath.Clean(trimmed)
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("%s binary %q is not accessible: %w", binaryName, resolved, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s binary path must not be a symlink: %q", binaryName, resolved)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s binary path points to a directory: %q", binaryName, resolved)
	}
	if err := validatePathChainNoSymlinks(resolved); err != nil {
		return "", fmt.Errorf("%s binary path chain is unsafe: %w", binaryName, err)
	}

	return resolved, nil
}

func normalizeProvider(provider runtime.Provider) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(string(provider)))
	switch runtime.Provider(trimmed) {
	case runtime.ProviderTelemt, runtime.ProviderMTG:
		return trimmed, nil
	case "":
		return "", errors.New("provider is required")
	default:
		return "", fmt.Errorf("unsupported provider for script adapter: %q", provider)
	}
}

func ParseInstallLifecycle(result execadapter.Result) InstallLifecycleSummary {
	summary := InstallLifecycleSummary{
		OperatorHints:    make([]string, 0, 4),
		ParseDiagnostics: make([]string, 0, 4),
	}

	lines := splitLifecycleLines(result.Stdout)
	expectProxyLink := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			expectProxyLink = false
			continue
		}

		switch {
		case strings.EqualFold(line, "Proxy link:"):
			expectProxyLink = true
			summary.SensitiveOutputPresent = true
			continue
		case strings.HasPrefix(line, "Install dir:"):
			summary.InstallDir = parseLifecycleField(line, "Install dir:")
			expectProxyLink = false
			continue
		case strings.HasPrefix(line, "Provider:"):
			summary.Provider = parseLifecycleField(line, "Provider:")
			expectProxyLink = false
			continue
		case strings.HasPrefix(line, "Public endpoint:"):
			summary.PublicEndpoint = parseLifecycleField(line, "Public endpoint:")
			expectProxyLink = false
			continue
		case strings.HasPrefix(line, "API:"):
			summary.APIEndpoint = parseLifecycleField(line, "API:")
			expectProxyLink = false
			continue
		case strings.HasPrefix(line, "Config:"):
			summary.ConfigPath = parseLifecycleField(line, "Config:")
			expectProxyLink = false
			continue
		case strings.HasPrefix(line, "Logs:"):
			summary.LogsHint = parseLifecycleField(line, "Logs:")
			expectProxyLink = false
			continue
		case strings.HasPrefix(line, "Secret:"):
			summary.Secret = parseLifecycleField(line, "Secret:")
			if summary.Secret != "" {
				summary.SensitiveOutputPresent = true
			}
			expectProxyLink = false
			continue
		}

		if strings.HasPrefix(line, "[FIX]") {
			appendUniqueText(&summary.OperatorHints, line)
			expectProxyLink = false
			continue
		}

		if expectProxyLink && installProxyLinkPattern.MatchString(line) {
			summary.ProxyLink = line
			summary.ProxyLinkPresent = true
			summary.SensitiveOutputPresent = true
			expectProxyLink = false
			continue
		}
		expectProxyLink = false
	}

	if !summary.ProxyLinkPresent {
		for _, rawLine := range lines {
			line := strings.TrimSpace(rawLine)
			if installProxyLinkPattern.MatchString(line) {
				summary.ProxyLink = line
				summary.ProxyLinkPresent = true
				summary.SensitiveOutputPresent = true
				break
			}
		}
	}

	if strings.TrimSpace(summary.Provider) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "provider marker is missing in install output")
	}
	if strings.TrimSpace(summary.InstallDir) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "install dir marker is missing in install output")
	}
	if strings.TrimSpace(summary.PublicEndpoint) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "public endpoint marker is missing in install output")
	}
	if strings.TrimSpace(summary.ConfigPath) == "" {
		appendUniqueText(&summary.ParseDiagnostics, "config path marker is missing in install output")
	}

	return summary
}

func splitLifecycleLines(raw string) []string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func parseLifecycleField(line string, prefix string) string {
	value := strings.TrimPrefix(line, prefix)
	return strings.TrimSpace(value)
}

func appendUniqueText(target *[]string, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	for _, existing := range *target {
		if existing == trimmed {
			return
		}
	}
	*target = append(*target, trimmed)
}

func normalizeLogValue(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	trimmedFallback := strings.TrimSpace(fallback)
	if trimmedFallback != "" {
		return trimmedFallback
	}
	return "n/a"
}

func putIfNotEmpty(values map[string]string, key string, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	values[key] = trimmed
}

func copyEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func sanitizeInstallEnvValueMap(overrides map[string]string) error {
	for key, value := range overrides {
		normalizedKey := strings.ToUpper(strings.TrimSpace(key))
		sanitizedValue, err := sanitizeInstallRawValue(normalizedKey, value)
		if err != nil {
			return err
		}

		switch normalizedKey {
		case "PORT", "API_PORT":
			sanitizedValue, err = sanitizePortValue(normalizedKey, sanitizedValue)
		case "TLS_DOMAIN":
			sanitizedValue, err = sanitizeTLSDomainValue(sanitizedValue)
		case "PUBLIC_IP":
			sanitizedValue, err = sanitizePublicIPValue(sanitizedValue)
		case "SECRET":
			sanitizedValue, err = sanitizeSecretValue(sanitizedValue)
		case "PROXY_USER":
			sanitizedValue, err = sanitizeProxyUserValue(sanitizedValue)
		case "TELEMT_IMAGE", "TELEMT_IMAGE_SOURCE", "MTG_IMAGE", "MTG_IMAGE_SOURCE":
			sanitizedValue, err = sanitizeImageReferenceValue(normalizedKey, sanitizedValue)
		}
		if err != nil {
			return err
		}
		overrides[key] = sanitizedValue
	}
	return nil
}

func sanitizePortValue(field string, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	numeric, err := strconv.Atoi(value)
	if err != nil || numeric < 1 || numeric > 65535 {
		return "", fmt.Errorf("invalid %s value %q", field, value)
	}
	return strconv.Itoa(numeric), nil
}

func sanitizeInstallRawValue(field string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if installValueUnsafeCharsPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid %s value: newline and quote characters are not allowed", field)
	}
	return trimmed, nil
}

func sanitizeTLSDomainValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	lower := strings.ToLower(value)
	if len(lower) > 253 {
		return "", fmt.Errorf("invalid TLS_DOMAIN value: exceeds max length")
	}
	if !tlsDomainPattern.MatchString(lower) {
		return "", fmt.Errorf("invalid TLS_DOMAIN value %q", value)
	}
	labels := strings.Split(lower, ".")
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("invalid TLS_DOMAIN value %q", value)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid TLS_DOMAIN value %q", value)
		}
		if len(label) > 63 {
			return "", fmt.Errorf("invalid TLS_DOMAIN value %q", value)
		}
	}
	return lower, nil
}

func sanitizePublicIPValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if ip := net.ParseIP(value); ip == nil {
		return "", fmt.Errorf("invalid PUBLIC_IP value %q", value)
	}
	return value, nil
}

func sanitizeSecretValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !secretValuePattern.MatchString(value) {
		return "", fmt.Errorf("invalid SECRET value format")
	}
	return value, nil
}

func sanitizeProxyUserValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !proxyUserValuePattern.MatchString(value) {
		return "", fmt.Errorf("invalid PROXY_USER value %q", value)
	}
	return value, nil
}

func sanitizeImageReferenceValue(field string, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !imageReferencePattern.MatchString(value) {
		return "", fmt.Errorf("invalid %s image reference %q", field, value)
	}
	if strings.Contains(value, "..") {
		return "", fmt.Errorf("invalid %s image reference %q", field, value)
	}
	return value, nil
}

func sanitizeEnvOverrides(
	commandName string,
	overrides map[string]string,
	allowedKeys []string,
	policy envOverrideTrustPolicy,
) (map[string]string, error) {
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}

	sanitized := make(map[string]string, len(overrides))
	seenNormalizedKeys := make(map[string]string, len(overrides))
	for key, value := range overrides {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}

		normalized := strings.ToUpper(trimmedKey)
		if !isAllowedOptInEnvKey(normalized, allowed, privilegedExecutionEnvOptInPrefixAllowlist) {
			return nil, fmt.Errorf("unsupported %s env override key %q", commandName, trimmedKey)
		}
		if existing, exists := seenNormalizedKeys[normalized]; exists && existing != trimmedKey {
			return nil, fmt.Errorf(
				"conflicting %s env override keys %q and %q",
				commandName,
				existing,
				trimmedKey,
			)
		}
		seenNormalizedKeys[normalized] = trimmedKey

		trimmedValue := strings.TrimSpace(value)
		if privilegedEnvValueUnsafePattern.MatchString(trimmedValue) {
			return nil, fmt.Errorf("unsafe %s env override value for key %q", commandName, trimmedKey)
		}

		sanitized[trimmedKey] = trimmedValue
	}

	if err := enforceTrustBoundaryEnvOverridePolicy(commandName, sanitized, policy); err != nil {
		return nil, err
	}

	return sanitized, nil
}

func enforceTrustBoundaryEnvOverridePolicy(
	commandName string,
	overrides map[string]string,
	policy envOverrideTrustPolicy,
) error {
	trustBoundaryKeys := make([]string, 0, len(overrides))
	for key := range overrides {
		normalized := normalizeEnvKey(key)
		if !isTrustBoundaryEnvOverrideKey(normalized) {
			continue
		}
		trustBoundaryKeys = append(trustBoundaryKeys, normalized)
	}
	sort.Strings(trustBoundaryKeys)
	trustBoundaryKeys = uniqueStrings(trustBoundaryKeys)

	if len(trustBoundaryKeys) == 0 {
		return nil
	}

	if !policy.AllowTrustBoundaryEnv {
		return fmt.Errorf(
			"%s env override keys cross trust boundary and require explicit allow flag: %s",
			commandName,
			strings.Join(trustBoundaryKeys, ", "),
		)
	}

	privilegedContext := strings.TrimSpace(policy.PrivilegedContext)
	if privilegedContext != "" {
		return fmt.Errorf(
			"%s env override keys cross trust boundary and are blocked in privileged context %q: %s",
			commandName,
			privilegedContext,
			strings.Join(trustBoundaryKeys, ", "),
		)
	}

	return nil
}

func normalizeEnvKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

func isTrustBoundaryEnvOverrideKey(normalizedKey string) bool {
	if normalizedKey == "" {
		return false
	}
	if _, ok := trustBoundaryEnvOverrideKeyAllowlist[normalizedKey]; ok {
		return true
	}
	for _, prefix := range trustBoundaryEnvOverridePrefixAllowlist {
		normalizedPrefix := normalizeEnvKey(prefix)
		if normalizedPrefix == "" {
			continue
		}
		if strings.HasPrefix(normalizedKey, normalizedPrefix) {
			return true
		}
	}
	return false
}

func detectPrivilegedExecutionContext() string {
	if value, ok := os.LookupEnv("SUDO_UID"); ok && strings.TrimSpace(value) != "" {
		return "sudo"
	}
	if value, ok := os.LookupEnv("SUDO_USER"); ok && strings.TrimSpace(value) != "" {
		return "sudo"
	}

	if value, ok := os.LookupEnv("CI"); ok {
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed != "" && trimmed != "0" && trimmed != "false" && trimmed != "no" {
			return "ci"
		}
	}

	if uid, ok := currentUserUID(); ok && uid == 0 {
		return "uid=0"
	}

	return ""
}

func isAllowedOptInEnvKey(normalizedKey string, allowlist map[string]struct{}, prefixAllowlist []string) bool {
	if normalizedKey == "" {
		return false
	}
	if _, ok := allowlist[normalizedKey]; ok {
		return true
	}
	for _, prefix := range prefixAllowlist {
		if strings.HasPrefix(normalizedKey, strings.ToUpper(strings.TrimSpace(prefix))) {
			return true
		}
	}
	return false
}

func mergeAllowedEnvKeys(groups ...[]string) []string {
	merged := make(map[string]struct{})
	for _, group := range groups {
		for _, key := range group {
			normalized := strings.ToUpper(strings.TrimSpace(key))
			if normalized == "" {
				continue
			}
			merged[normalized] = struct{}{}
		}
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func installDirForLog(installDir string) string {
	resolved, err := resolveInstallDirPath(installDir)
	if err == nil {
		return resolved
	}
	return runtime.DefaultInstallDir
}

func normalizeInstallDirValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = runtime.DefaultInstallDir
	}
	return pathutil.CleanPath(trimmed)
}

func resolveInstallDirPath(value string) (string, error) {
	normalized := normalizeInstallDirValue(value)
	absolute, err := pathutil.ResolvePath(normalized)
	if err != nil {
		return "", fmt.Errorf("unable to resolve INSTALL_DIR %q: %w", normalized, err)
	}
	return absolute, nil
}

func enforceInstallDirPathSafety(installDir string, allowMissingTarget bool) error {
	if err := validateInstallDirPathSafety(installDir); err != nil {
		return err
	}
	if err := validatePathChainNoSymlinks(installDir); err != nil {
		return fmt.Errorf("invalid INSTALL_DIR %q: %w", installDir, err)
	}
	if allowMissingTarget {
		if err := ensurePathNotSymlinkIfExists(installDir, "INSTALL_DIR"); err != nil {
			return err
		}
	} else {
		if err := ensurePathNotSymlink(installDir, "INSTALL_DIR"); err != nil {
			return err
		}
	}

	parent := filepath.Dir(installDir)
	if canonicalPathKey(parent) != canonicalPathKey(installDir) {
		if err := validatePathChainNoSymlinks(parent); err != nil {
			return fmt.Errorf("invalid INSTALL_DIR %q: parent path is unsafe: %w", installDir, err)
		}
		if allowMissingTarget {
			if err := ensurePathNotSymlinkIfExists(parent, "INSTALL_DIR parent directory"); err != nil {
				return err
			}
		} else {
			if err := ensurePathNotSymlink(parent, "INSTALL_DIR parent directory"); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateInstallDirPathSafety(installDir string) error {
	normalized := canonicalPathKey(installDir)
	if normalized == "" || normalized == "." {
		return fmt.Errorf("invalid INSTALL_DIR %q: empty path is not allowed", installDir)
	}
	if _, blocked := protectedInstallDirTargets[normalized]; blocked {
		return fmt.Errorf("invalid INSTALL_DIR %q: refusing to operate on protected system path", installDir)
	}

	volume := filepath.VolumeName(installDir)
	if volume != "" {
		volumeRoot := filepath.Clean(volume + string(os.PathSeparator))
		if strings.EqualFold(filepath.Clean(installDir), volumeRoot) {
			return fmt.Errorf("invalid INSTALL_DIR %q: refusing to operate on volume root", installDir)
		}
	}

	return nil
}

func validatePathChainNoSymlinks(path string) error {
	resolved, err := resolveInstallDirPath(path)
	if err != nil {
		return err
	}

	trimmed := strings.TrimSpace(resolved)
	if trimmed == "" {
		return fmt.Errorf("empty path")
	}

	volume := filepath.VolumeName(trimmed)
	segments := splitPathSegments(trimmed, volume)

	current := volume
	if current == "" {
		if filepath.IsAbs(trimmed) {
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

func splitPathSegments(path string, volume string) []string {
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

func ensurePathNotSymlink(path string, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s %q is not accessible: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	return nil
}

func ensurePathNotSymlinkIfExists(path string, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s %q is not accessible: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	return nil
}

func requireRuntimeDirectory(path string, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s %q is not accessible: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %q is not a directory", label, path)
	}
	if err := ensurePathNotSymlink(path, label); err != nil {
		return err
	}
	return nil
}

func canonicalPathKey(path string) string {
	return pathutil.CanonicalPathKey(path)
}

func isPathWithin(base string, target string) bool {
	baseResolved, err := resolvePathBoundary(base)
	if err != nil {
		return false
	}
	targetResolved, err := resolvePathBoundary(target)
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

func resolvePathBoundary(path string) (string, error) {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	absolute, absErr := filepath.Abs(cleaned)
	if absErr != nil {
		return "", absErr
	}
	return filepath.Clean(absolute), nil
}

func fallbackLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
