package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/pathutil"
	"mtproxy-installer/app/internal/runtime"
)

var trustedDockerBinaryCandidates = []string{
	"/usr/bin/docker",
	"/usr/local/bin/docker",
	"/bin/docker",
}

var composeEnvOverrideAllowlist = []string{
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

var composeEnvOverridePrefixAllowlist = []string{
	"DOCKER_TLS_",
}

var composeSubcommandAllowlist = map[string]struct{}{
	"ps":      {},
	"logs":    {},
	"restart": {},
	"up":      {},
	"down":    {},
}

type composeFlagRule struct {
	expectsValue  bool
	validateValue func(string) error
}

var composeArgRuleSet = map[string]map[string]composeFlagRule{
	"ps": {
		"-a":         {},
		"--all":      {},
		"-q":         {},
		"--quiet":    {},
		"--services": {},
	},
	"logs": {
		"-f":           {},
		"--follow":     {},
		"-t":           {},
		"--timestamps": {},
		"--no-color":   {},
		"--tail": {
			expectsValue:  true,
			validateValue: validateComposeTailValue,
		},
	},
	"restart": {
		"--timeout": {
			expectsValue:  true,
			validateValue: validateComposeTimeoutValue,
		},
	},
	"up": {
		"-d":               {},
		"--detach":         {},
		"--build":          {},
		"--no-build":       {},
		"--force-recreate": {},
		"--no-recreate":    {},
		"--no-deps":        {},
		"--remove-orphans": {},
		"--pull": {
			expectsValue:  true,
			validateValue: validateComposePullValue,
		},
	},
	"down": {
		"-v":               {},
		"--volumes":        {},
		"--remove-orphans": {},
		"--timeout": {
			expectsValue:  true,
			validateValue: validateComposeTimeoutValue,
		},
	},
}

var (
	composeUnsafeTokenPattern  = regexp.MustCompile(`[\r\n"'` + "`" + `]`)
	composeUnsafeEnvValueToken = regexp.MustCompile(`[\r\n]`)
	composeServiceTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	composeSimpleValuePattern  = regexp.MustCompile(`^[A-Za-z0-9._:/@=-]{1,64}$`)
)

type ComposeAdapterOptions struct {
	Runner  *execadapter.Runner
	Logger  *slog.Logger
	Runtime *runtime.RuntimeInstallation
	Binary  string
}

type ComposeAdapter struct {
	runner            *execadapter.Runner
	logger            *slog.Logger
	runtimeInstallDir string
	expectedRuntime   runtimeExecutionSnapshot
	binary            string
}

type runtimeExecutionSnapshot struct {
	installDir  string
	composeFile string
	envFile     string
	provider    runtime.Provider
}

type ComposeCommand struct {
	Subcommand              string
	Args                    []string
	Services                []string
	WorkingDir              string
	EnvOverrides            map[string]string
	Stdout                  io.Writer
	Stderr                  io.Writer
	DisableStdoutCapture    bool
	DisableStderrCapture    bool
	DisableStderrSummaryLog bool
}

func NewComposeAdapter(options ComposeAdapterOptions) (*ComposeAdapter, error) {
	logger := fallbackLogger(options.Logger)

	if options.Runtime == nil {
		return nil, errors.New("runtime installation is required for compose adapter")
	}
	if strings.TrimSpace(options.Runtime.Paths.ComposeFile) == "" {
		return nil, errors.New("runtime compose file path is required")
	}
	if strings.TrimSpace(options.Runtime.Paths.EnvFile) == "" {
		return nil, errors.New("runtime env file path is required")
	}
	if strings.TrimSpace(options.Runtime.Paths.InstallDir) == "" {
		return nil, errors.New("runtime install dir is required")
	}

	binary, err := resolveTrustedBinaryPath("docker", options.Binary, trustedDockerBinaryCandidates)
	if err != nil {
		return nil, err
	}

	runner := options.Runner
	if runner == nil {
		runner = execadapter.NewRunner(logger)
	}

	expectedRuntime, err := hardenRuntimeSnapshot(runtimeExecutionSnapshot{
		installDir:  options.Runtime.Paths.InstallDir,
		composeFile: options.Runtime.Paths.ComposeFile,
		envFile:     options.Runtime.Paths.EnvFile,
		provider:    options.Runtime.Provider.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("compose runtime hardening failed: %w", err)
	}

	logger.Debug(
		"compose adapter initialized",
		"binary", binary,
		"compose_file", expectedRuntime.composeFile,
		"project_directory", expectedRuntime.installDir,
		"env_file", expectedRuntime.envFile,
	)

	return &ComposeAdapter{
		runner:            runner,
		logger:            logger,
		runtimeInstallDir: expectedRuntime.installDir,
		expectedRuntime:   expectedRuntime,
		binary:            binary,
	}, nil
}

func (a *ComposeAdapter) Run(ctx context.Context, command ComposeCommand) (execadapter.Result, error) {
	runtimeSnapshot, err := a.recheckRuntimeAtExecution()
	if err != nil {
		return execadapter.Result{}, err
	}

	subcommand, err := normalizeComposeSubcommand(command.Subcommand)
	if err != nil {
		return execadapter.Result{}, err
	}
	args, err := validateComposeArgs(subcommand, command.Args)
	if err != nil {
		return execadapter.Result{}, err
	}
	services, err := validateComposeServices(command.Services)
	if err != nil {
		return execadapter.Result{}, err
	}

	workingDir := strings.TrimSpace(command.WorkingDir)
	if workingDir == "" {
		workingDir = runtimeSnapshot.installDir
	}

	argv := make([]string, 0, 12+len(args)+len(services))
	argv = append(argv,
		"compose",
		"-f", runtimeSnapshot.composeFile,
		"--project-directory", runtimeSnapshot.installDir,
		"--env-file", runtimeSnapshot.envFile,
		subcommand,
	)
	argv = append(argv, args...)
	argv = append(argv, services...)

	envOverrides, err := sanitizeComposeEnvOverrides(command.EnvOverrides, composeEnvOverrideAllowlist)
	if err != nil {
		return execadapter.Result{}, err
	}

	runtimeSnapshot, err = a.recheckRuntimeAtExecution()
	if err != nil {
		return execadapter.Result{}, err
	}
	argv[2] = runtimeSnapshot.composeFile
	argv[4] = runtimeSnapshot.installDir
	argv[6] = runtimeSnapshot.envFile
	if strings.TrimSpace(command.WorkingDir) == "" {
		workingDir = runtimeSnapshot.installDir
	}

	redactedArgs := execadapter.RedactArgs(argv)
	redactedEnv := execadapter.RedactEnvSnapshot(envOverrides)
	envKeys := sortedKeys(envOverrides)

	a.logger.Debug(
		"compose adapter request assembled",
		"binary", a.binary,
		"subcommand", subcommand,
		"services", services,
		"args", redactedArgs,
		"working_dir", workingDir,
		"compose_file", runtimeSnapshot.composeFile,
		"project_directory", runtimeSnapshot.installDir,
		"env_file", runtimeSnapshot.envFile,
		"provider", runtimeSnapshot.provider,
		"env_override_keys", envKeys,
		"env_overrides", redactedEnv,
	)
	a.logger.Info(
		"compose adapter start",
		"binary", a.binary,
		"subcommand", subcommand,
		"services", services,
		"args", redactedArgs,
		"working_dir", workingDir,
		"compose_file", runtimeSnapshot.composeFile,
		"env_file", runtimeSnapshot.envFile,
		"provider", runtimeSnapshot.provider,
	)

	result, runErr := a.runner.Run(ctx, execadapter.Request{
		Command:              a.binary,
		Args:                 argv,
		WorkingDir:           workingDir,
		EnvOverrides:         envOverrides,
		InheritParentEnv:     false,
		AllowedEnvKeys:       sortedKeys(envOverrides),
		UseSafePath:          true,
		Stdout:               command.Stdout,
		Stderr:               command.Stderr,
		DisableStdoutCapture: command.DisableStdoutCapture,
		DisableStderrCapture: command.DisableStderrCapture,
	})
	if runErr != nil {
		failureLogArgs := []any{
			"binary", a.binary,
			"subcommand", subcommand,
			"services", services,
			"args", redactedArgs,
			"working_dir", workingDir,
			"compose_file", runtimeSnapshot.composeFile,
			"env_file", runtimeSnapshot.envFile,
			"provider", runtimeSnapshot.provider,
			"elapsed", result.Elapsed,
			"exit_status", result.ExitCode,
		}
		if !command.DisableStderrSummaryLog {
			failureLogArgs = append(failureLogArgs, "stderr_summary", result.StderrSummary)
		}
		failureLogArgs = append(failureLogArgs, "error", execadapter.RedactText(runErr.Error()))

		a.logger.Error("compose adapter failed", failureLogArgs...)
		return result, runErr
	}

	finishLogArgs := []any{
		"binary", a.binary,
		"subcommand", subcommand,
		"services", services,
		"args", redactedArgs,
		"working_dir", workingDir,
		"compose_file", runtimeSnapshot.composeFile,
		"env_file", runtimeSnapshot.envFile,
		"provider", runtimeSnapshot.provider,
		"elapsed", result.Elapsed,
		"exit_status", result.ExitCode,
	}
	if !command.DisableStderrSummaryLog {
		finishLogArgs = append(finishLogArgs, "stderr_summary", result.StderrSummary)
	}

	a.logger.Info("compose adapter finish", finishLogArgs...)

	return result, nil
}

func fallbackLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sanitizeComposeEnvOverrides(overrides map[string]string, allowedKeys []string) (map[string]string, error) {
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
		if !isAllowedComposeOptInKey(normalized, allowed, composeEnvOverridePrefixAllowlist) {
			return nil, fmt.Errorf("unsupported compose env override key %q", trimmedKey)
		}
		if existing, exists := seenNormalizedKeys[normalized]; exists && existing != trimmedKey {
			return nil, fmt.Errorf(
				"conflicting compose env override keys %q and %q",
				existing,
				trimmedKey,
			)
		}
		seenNormalizedKeys[normalized] = trimmedKey

		trimmedValue := strings.TrimSpace(value)
		if composeUnsafeEnvValueToken.MatchString(trimmedValue) {
			return nil, fmt.Errorf("compose env override %q contains unsafe characters", trimmedKey)
		}
		sanitized[trimmedKey] = trimmedValue
	}

	return sanitized, nil
}

func isAllowedComposeOptInKey(normalizedKey string, allowlist map[string]struct{}, prefixAllowlist []string) bool {
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

func (a *ComposeAdapter) recheckRuntimeAtExecution() (runtimeExecutionSnapshot, error) {
	installDir := strings.TrimSpace(a.runtimeInstallDir)
	if installDir == "" {
		return runtimeExecutionSnapshot{}, errors.New("runtime install dir is required")
	}

	loaded, err := runtime.Load(runtime.LoadOptions{
		InstallDir: installDir,
		Logger:     a.logger,
	})
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("compose runtime recheck failed for %q: %w", installDir, err)
	}

	snapshot := runtimeExecutionSnapshot{
		installDir:  loaded.Paths.InstallDir,
		composeFile: loaded.Paths.ComposeFile,
		envFile:     loaded.Paths.EnvFile,
		provider:    loaded.Provider.Name,
	}
	snapshot, err = hardenRuntimeSnapshot(snapshot)
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"compose runtime recheck failed for %q: %w",
			installDir,
			err,
		)
	}

	originalInstallDir := strings.TrimSpace(a.expectedRuntime.installDir)
	if originalInstallDir != "" && canonicalPathKey(originalInstallDir) != canonicalPathKey(snapshot.installDir) {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"compose runtime recheck failed for %q: install dir changed to %q",
			originalInstallDir,
			snapshot.installDir,
		)
	}

	originalComposeFile := strings.TrimSpace(a.expectedRuntime.composeFile)
	if originalComposeFile != "" && canonicalPathKey(originalComposeFile) != canonicalPathKey(snapshot.composeFile) {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"compose runtime recheck failed for %q: compose file changed to %q",
			originalComposeFile,
			snapshot.composeFile,
		)
	}

	originalEnvFile := strings.TrimSpace(a.expectedRuntime.envFile)
	if originalEnvFile != "" && canonicalPathKey(originalEnvFile) != canonicalPathKey(snapshot.envFile) {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"compose runtime recheck failed for %q: env file changed to %q",
			originalEnvFile,
			snapshot.envFile,
		)
	}
	if !strings.EqualFold(string(a.expectedRuntime.provider), string(snapshot.provider)) {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"compose runtime recheck failed for %q: provider changed from %q to %q",
			installDir,
			a.expectedRuntime.provider,
			snapshot.provider,
		)
	}

	return snapshot, nil
}

func canonicalPathKey(path string) string {
	return pathutil.CanonicalPathKey(path)
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

func hardenRuntimeSnapshot(snapshot runtimeExecutionSnapshot) (runtimeExecutionSnapshot, error) {
	resolvedInstallDir, err := resolveAbsolutePath(snapshot.installDir)
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("invalid runtime install dir %q: %w", snapshot.installDir, err)
	}
	resolvedComposeFile, err := resolveAbsolutePath(snapshot.composeFile)
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("invalid runtime compose file %q: %w", snapshot.composeFile, err)
	}
	resolvedEnvFile, err := resolveAbsolutePath(snapshot.envFile)
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("invalid runtime env file %q: %w", snapshot.envFile, err)
	}

	if err := validatePathChainNoSymlinks(resolvedInstallDir); err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime install dir path chain is unsafe: %w", err)
	}
	if err := ensurePathNotSymlink(resolvedInstallDir, "runtime install dir"); err != nil {
		return runtimeExecutionSnapshot{}, err
	}
	if err := validatePathChainNoSymlinks(resolvedComposeFile); err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime compose file path chain is unsafe: %w", err)
	}
	if err := ensurePathNotSymlink(resolvedComposeFile, "runtime compose file"); err != nil {
		return runtimeExecutionSnapshot{}, err
	}
	if err := validatePathChainNoSymlinks(resolvedEnvFile); err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime env file path chain is unsafe: %w", err)
	}
	if err := ensurePathNotSymlink(resolvedEnvFile, "runtime env file"); err != nil {
		return runtimeExecutionSnapshot{}, err
	}

	if !isPathWithin(resolvedInstallDir, resolvedComposeFile) {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"runtime compose file %q escapes install dir %q",
			resolvedComposeFile,
			resolvedInstallDir,
		)
	}
	if !isPathWithin(resolvedInstallDir, resolvedEnvFile) {
		return runtimeExecutionSnapshot{}, fmt.Errorf(
			"runtime env file %q escapes install dir %q",
			resolvedEnvFile,
			resolvedInstallDir,
		)
	}

	composeInfo, err := os.Stat(resolvedComposeFile)
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime compose file %q is not accessible: %w", resolvedComposeFile, err)
	}
	if composeInfo.IsDir() {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime compose file %q is a directory", resolvedComposeFile)
	}
	envInfo, err := os.Stat(resolvedEnvFile)
	if err != nil {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime env file %q is not accessible: %w", resolvedEnvFile, err)
	}
	if envInfo.IsDir() {
		return runtimeExecutionSnapshot{}, fmt.Errorf("runtime env file %q is a directory", resolvedEnvFile)
	}

	snapshot.installDir = resolvedInstallDir
	snapshot.composeFile = resolvedComposeFile
	snapshot.envFile = resolvedEnvFile
	return snapshot, nil
}

func resolveAbsolutePath(path string) (string, error) {
	return pathutil.ResolvePath(path)
}

func validatePathChainNoSymlinks(path string) error {
	resolved, err := resolveAbsolutePath(path)
	if err != nil {
		return err
	}

	volume := filepath.VolumeName(resolved)
	segments := splitPathSegments(resolved, volume)
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

func isPathWithin(base string, target string) bool {
	baseResolved, err := resolveAbsolutePath(base)
	if err != nil {
		return false
	}
	targetResolved, err := resolveAbsolutePath(target)
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

func normalizeComposeSubcommand(raw string) (string, error) {
	subcommand := strings.ToLower(strings.TrimSpace(raw))
	if subcommand == "" {
		return "", errors.New("compose subcommand is required")
	}
	if _, ok := composeSubcommandAllowlist[subcommand]; !ok {
		return "", fmt.Errorf("unsupported compose subcommand %q", raw)
	}
	return subcommand, nil
}

func validateComposeArgs(subcommand string, args []string) ([]string, error) {
	rules := composeArgRuleSet[subcommand]
	validated := make([]string, 0, len(args))

	for idx := 0; idx < len(args); idx++ {
		token := strings.TrimSpace(args[idx])
		if token == "" {
			return nil, fmt.Errorf("compose arg #%d is empty", idx)
		}
		if composeUnsafeTokenPattern.MatchString(token) {
			return nil, fmt.Errorf("compose arg %q contains unsafe characters", token)
		}
		if !strings.HasPrefix(token, "-") {
			return nil, fmt.Errorf("compose arg %q is not allowed; pass services via Services only", token)
		}

		flag := token
		value := ""
		hasInlineValue := false
		if strings.Contains(token, "=") {
			var ok bool
			flag, value, ok = strings.Cut(token, "=")
			if !ok {
				return nil, fmt.Errorf("invalid compose arg %q", token)
			}
			hasInlineValue = true
			value = strings.TrimSpace(value)
		}

		rule, ok := rules[flag]
		if !ok {
			return nil, fmt.Errorf("unsupported compose arg %q for subcommand %q", flag, subcommand)
		}

		if hasInlineValue {
			if !rule.expectsValue {
				return nil, fmt.Errorf("compose arg %q does not accept a value", flag)
			}
			if value == "" {
				return nil, fmt.Errorf("compose arg %q requires a value", flag)
			}
			if err := validateComposeArgValue(flag, value, rule); err != nil {
				return nil, err
			}
			validated = append(validated, flag+"="+value)
			continue
		}

		validated = append(validated, flag)
		if !rule.expectsValue {
			continue
		}

		nextIdx := idx + 1
		if nextIdx >= len(args) {
			return nil, fmt.Errorf("compose arg %q requires a value", flag)
		}

		value = strings.TrimSpace(args[nextIdx])
		if value == "" {
			return nil, fmt.Errorf("compose arg %q requires a non-empty value", flag)
		}
		if strings.HasPrefix(value, "-") {
			return nil, fmt.Errorf("compose arg %q value %q is invalid", flag, value)
		}
		if composeUnsafeTokenPattern.MatchString(value) {
			return nil, fmt.Errorf("compose arg %q value %q contains unsafe characters", flag, value)
		}
		if err := validateComposeArgValue(flag, value, rule); err != nil {
			return nil, err
		}
		validated = append(validated, value)
		idx = nextIdx
	}

	return validated, nil
}

func validateComposeServices(services []string) ([]string, error) {
	validated := make([]string, 0, len(services))
	for _, service := range services {
		trimmed := strings.TrimSpace(service)
		if trimmed == "" {
			return nil, errors.New("compose service name must not be empty")
		}
		if strings.HasPrefix(trimmed, "-") {
			return nil, fmt.Errorf("compose service %q is invalid: must not start with '-'", trimmed)
		}
		if composeUnsafeTokenPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("compose service %q contains unsafe characters", trimmed)
		}
		if !composeServiceTokenPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("compose service %q has unsupported format", trimmed)
		}
		validated = append(validated, trimmed)
	}
	return validated, nil
}

func validateComposeArgValue(flag string, value string, rule composeFlagRule) error {
	if rule.validateValue != nil {
		return rule.validateValue(value)
	}
	if !composeSimpleValuePattern.MatchString(value) {
		return fmt.Errorf("compose arg %q value %q has unsupported format", flag, value)
	}
	return nil
}

func validateComposeTailValue(value string) error {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "all" {
		return nil
	}
	numeric, err := strconv.Atoi(lower)
	if err != nil || numeric < 0 {
		return fmt.Errorf("compose logs --tail value %q is invalid", value)
	}
	return nil
}

func validateComposeTimeoutValue(value string) error {
	numeric, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || numeric < 0 {
		return fmt.Errorf("compose timeout value %q is invalid", value)
	}
	return nil
}

func validateComposePullValue(value string) error {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "always", "missing", "never":
		return nil
	default:
		return fmt.Errorf("compose --pull value %q is invalid", value)
	}
}

func FormatComposeCommandPreview(binary string, args []string) string {
	joined := strings.Join(execadapter.RedactArgs(args), " ")
	return fmt.Sprintf("%s %s", strings.TrimSpace(binary), strings.TrimSpace(joined))
}
