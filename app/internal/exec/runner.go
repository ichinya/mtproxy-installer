package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	osexec "os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultStderrSummaryLimit = 320
	normalizedExecutablePath  = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

var (
	proxyLinkPattern           = regexp.MustCompile(`(?i)tg://proxy\?[^\s]+|https://t\.me/proxy\?\S+`)
	bearerTokenPattern         = regexp.MustCompile(`(?i)\b(bearer)\s+([A-Za-z0-9\-._~+/]+=*)`)
	authorizationHeaderPattern = regexp.MustCompile(`(?i)\b(authorization)\s*:\s*[^|]+`)
	cookieHeaderPattern        = regexp.MustCompile(`(?i)\b(cookie)\s*:\s*[^|]+`)
	setCookieHeaderPattern     = regexp.MustCompile(`(?i)\b(set-cookie)\s*:\s*[^|]+`)
	sensitiveKeyNamePattern    = regexp.MustCompile(`(?i)(^AUTH|(?:^|[_-])(TOKEN|SECRET|PASSWORD|PASSWD|PASS|COOKIE|SESSION|CREDENTIAL)(?:$|[_-])|(?:^|[_-])KEY(?:$|[_-]))`)
	sensitiveAssignmentPattern = regexp.MustCompile(`(?i)\b([A-Z0-9_.-]*(?:TOKEN|SECRET|PASSWORD|PASSWD|PASS|COOKIE|SESSION|CREDENTIAL|AUTH[A-Z0-9_.-]*|[A-Z0-9_.-]*_KEY|APIKEY|API_KEY|PRIVATE_KEY))\s*([:=])\s*("[^"]*"|'[^']*'|[^\s|,;]+)`)
	sensitiveOptionPattern     = regexp.MustCompile(`(?i)(--?[a-z0-9_.-]*(?:token|secret|password|passwd|pass|cookie|auth|session|credential|api[_-]?key|private[_-]?key|_key))([=\s]+)("[^"]*"|'[^']*'|[^\s|,;]+)`)
	sensitiveHeaderPattern     = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:token|secret|password|passwd|cookie|auth|api[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*)\s*:\s*("[^"]*"|'[^']*'|[^\s|,;]+)`)
	sensitiveJSONPattern       = regexp.MustCompile(`(?i)"([a-z0-9_.-]*(?:token|secret|password|passwd|cookie|auth|api[_-]?key|private[_-]?key)[a-z0-9_.-]*)"\s*:\s*"(?:\\.|[^"\\])*"`)
	sensitiveQueryPattern      = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:token|secret|password|passwd|pass|cookie|auth|session|credential|api[_-]?key|private[_-]?key|_key)[a-z0-9_.-]*)=([^&\s|]+)`)
	unsafeEnvValuePattern      = regexp.MustCompile(`[\x00\r\n]`)

	blockedEnvPrefixes = []string{"BASH_FUNC_"}
	blockedEnvKeys     = map[string]struct{}{
		"BASH_ENV":        {},
		"ENV":             {},
		"CDPATH":          {},
		"GLOBIGNORE":      {},
		"SHELLOPTS":       {},
		"BASHOPTS":        {},
		"LD_PRELOAD":      {},
		"LD_LIBRARY_PATH": {},
	}

	sensitiveInheritedEnvKeys = map[string]struct{}{
		"HOME":                 {},
		"XDG_RUNTIME_DIR":      {},
		"DOCKER_HOST":          {},
		"DOCKER_CONTEXT":       {},
		"DOCKER_TLS":           {},
		"DOCKER_CERT_PATH":     {},
		"DOCKER_TLS_VERIFY":    {},
		"COMPOSE_PROJECT_NAME": {},
		"COMPOSE_PROFILES":     {},
	}
	sensitiveInheritedEnvPrefixes = []string{
		"DOCKER_",
		"DOCKER_TLS_",
		"COMPOSE_",
	}
)

type Runner struct {
	logger *slog.Logger
}

type Request struct {
	Command            string
	Args               []string
	WorkingDir         string
	EnvOverrides       map[string]string
	InheritParentEnv   bool
	AllowedEnvKeys     []string
	AllowedEnvPrefixes []string
	UseSafePath        bool
	TrustedPath        string
	StderrSummaryLimit int
	LogSuccess         bool
}

type Result struct {
	Command       string
	Args          []string
	RedactedArgs  []string
	WorkingDir    string
	Stdout        string
	Stderr        string
	StderrSummary string
	ExitCode      int
	StartedAt     time.Time
	Elapsed       time.Duration
}

type CommandError struct {
	Result Result
	Err    error
}

func (e *CommandError) Error() string {
	if e == nil {
		return "external command failed"
	}
	if e.Err == nil {
		return "external command failed"
	}
	errMessage := RedactText(e.Err.Error())
	stderrSummary := RedactText(strings.TrimSpace(e.Result.StderrSummary))
	if stderrSummary == "" {
		return fmt.Sprintf("external command failed: %s", errMessage)
	}
	if e.Result.StderrSummary == "" {
		return fmt.Sprintf("external command failed: %s", errMessage)
	}
	return fmt.Sprintf("external command failed: %s (%s)", errMessage, stderrSummary)
}

func (e *CommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewRunner(logger *slog.Logger) *Runner {
	return &Runner{
		logger: fallbackLogger(logger),
	}
}

func (r *Runner) Run(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	command := strings.TrimSpace(request.Command)
	if command == "" {
		return Result{}, &CommandError{
			Err: errors.New("command is required"),
		}
	}

	workingDir := strings.TrimSpace(request.WorkingDir)
	if workingDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			workingDir = "."
		} else {
			workingDir = cwd
		}
	}

	summaryLimit := request.StderrSummaryLimit
	if summaryLimit <= 0 {
		summaryLimit = defaultStderrSummaryLimit
	}

	redactedArgs := RedactArgs(request.Args)
	redactedEnv := RedactEnvSnapshot(request.EnvOverrides)
	envKeys := sortedKeys(request.EnvOverrides)
	envAllowlist := sortedUniqueKeys(request.AllowedEnvKeys)
	envAllowPrefixes := sortedUniqueEnvPrefixes(request.AllowedEnvPrefixes)
	if len(request.EnvOverrides) > 0 && len(envAllowlist) == 0 && len(envAllowPrefixes) == 0 {
		return Result{}, &CommandError{
			Err: errors.New("AllowedEnvKeys or AllowedEnvPrefixes is required when EnvOverrides are provided"),
		}
	}
	if unsupported := unsupportedEnvKeys(request.EnvOverrides, envAllowlist, envAllowPrefixes); len(unsupported) > 0 {
		return Result{}, &CommandError{
			Err: fmt.Errorf("env override keys are not allowed: %s", strings.Join(unsupported, ", ")),
		}
	}
	if err := validateSensitiveEnvOverrideOptIn(request.EnvOverrides, envAllowlist); err != nil {
		return Result{}, &CommandError{
			Err: err,
		}
	}
	if err := validateEnvOverrideValues(request.EnvOverrides); err != nil {
		return Result{}, &CommandError{
			Err: err,
		}
	}
	baseEnv := []string(nil)
	strippedInheritedSensitiveEnvKeys := []string(nil)
	if request.InheritParentEnv {
		if len(envAllowlist) == 0 && len(envAllowPrefixes) == 0 {
			return Result{}, &CommandError{
				Err: errors.New("AllowedEnvKeys or AllowedEnvPrefixes is required when InheritParentEnv is enabled"),
			}
		}
		if disallowedInheritedSelections := disallowedInheritedSensitiveSelections(envAllowlist, envAllowPrefixes); len(disallowedInheritedSelections) > 0 {
			return Result{}, &CommandError{
				Err: fmt.Errorf(
					"inherited sensitive env selection is not allowed: %s; use explicit EnvOverrides opt-in",
					strings.Join(disallowedInheritedSelections, ", "),
				),
			}
		}
		baseEnv = collectAllowedParentEnv(envAllowlist, envAllowPrefixes)
		baseEnv, strippedInheritedSensitiveEnvKeys = filterInheritedSensitiveRuntimeEnv(baseEnv)
	}
	commandEnv, blockedEnv := buildCommandEnv(
		baseEnv,
		envAllowlist,
		envAllowPrefixes,
		request.EnvOverrides,
		request.UseSafePath,
		request.TrustedPath,
	)

	r.logger.Debug(
		"external command start",
		"command", command,
		"args", redactedArgs,
		"working_dir", workingDir,
		"inherit_parent_env", request.InheritParentEnv,
		"env_allowlist", envAllowlist,
		"env_allow_prefixes", envAllowPrefixes,
		"blocked_env_keys", blockedEnv,
		"stripped_inherited_sensitive_env_keys", strippedInheritedSensitiveEnvKeys,
		"normalized_path", normalizedExecutablePath,
		"use_safe_path", request.UseSafePath,
		"trusted_path", strings.TrimSpace(request.TrustedPath),
		"env_override_keys", envKeys,
		"env_overrides", redactedEnv,
	)

	startedAt := time.Now()

	cmd := osexec.CommandContext(ctx, command, request.Args...)
	cmd.Dir = workingDir
	cmd.Env = commandEnv

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()
	elapsed := time.Since(startedAt)

	stderr := stderrBuf.String()
	exitCode := exitCodeFromRunErr(runErr)

	result := Result{
		Command:       command,
		Args:          append([]string(nil), request.Args...),
		RedactedArgs:  redactedArgs,
		WorkingDir:    workingDir,
		Stdout:        stdoutBuf.String(),
		Stderr:        stderr,
		StderrSummary: SummarizeStderr(stderr, summaryLimit),
		ExitCode:      exitCode,
		StartedAt:     startedAt,
		Elapsed:       elapsed,
	}
	result.StderrSummary = RedactText(result.StderrSummary)

	finishLevel := slog.LevelDebug
	if runErr != nil {
		finishLevel = slog.LevelError
	} else if request.LogSuccess {
		finishLevel = slog.LevelInfo
	}

	r.logger.Log(
		ctx,
		finishLevel,
		"external command finish",
		"command", command,
		"args", redactedArgs,
		"working_dir", workingDir,
		"elapsed", elapsed,
		"exit_status", exitCode,
		"stderr_summary", result.StderrSummary,
	)

	if runErr != nil {
		return result, &CommandError{
			Result: result,
			Err:    runErr,
		}
	}

	return result, nil
}

func SummarizeStderr(raw string, limit int) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}

	if limit <= 0 {
		limit = defaultStderrSummaryLimit
	}

	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	lines := strings.Split(cleaned, "\n")
	if len(lines) > 3 {
		lines = lines[:3]
	}

	normalized := strings.Join(lines, " | ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	normalized = RedactText(normalized)

	if len([]rune(normalized)) <= limit {
		return normalized
	}

	if limit <= 3 {
		return string([]rune(normalized)[:limit])
	}

	runes := []rune(normalized)
	return string(runes[:limit-3]) + "..."
}

func RedactArgs(args []string) []string {
	redacted := make([]string, len(args))
	expectSecretValue := false

	for i, arg := range args {
		trimmed := strings.TrimSpace(arg)

		if expectSecretValue {
			redacted[i] = "[redacted]"
			expectSecretValue = false
			continue
		}

		if trimmed == "" {
			redacted[i] = arg
			continue
		}

		if proxyLinkPattern.MatchString(trimmed) {
			redacted[i] = "[redacted-proxy-link]"
			continue
		}

		if key, value, ok := strings.Cut(trimmed, "="); ok {
			normalizedKey := strings.TrimLeft(key, "-")
			if isSensitiveKey(normalizedKey) {
				redacted[i] = key + "=[redacted]"
				continue
			}
			redacted[i] = key + "=" + RedactValue(normalizedKey, value)
			continue
		}

		if isSensitiveOption(trimmed) {
			redacted[i] = trimmed
			expectSecretValue = true
			continue
		}

		redacted[i] = RedactValue("", trimmed)
	}

	return redacted
}

func RedactEnvSnapshot(overrides map[string]string) map[string]string {
	redacted := make(map[string]string, len(overrides))
	for key, value := range overrides {
		redacted[key] = RedactValue(key, value)
	}
	return redacted
}

func RedactValue(key string, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	if isSensitiveKey(key) {
		return "[redacted]"
	}
	return RedactText(trimmed)
}

func RedactText(value string) string {
	return redactSensitiveText(value)
}

func exitCodeFromRunErr(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}

	return -1
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUniqueKeys(values []string) []string {
	uniq := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		uniq[trimmed] = struct{}{}
	}

	keys := make([]string, 0, len(uniq))
	for key := range uniq {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUniqueEnvPrefixes(values []string) []string {
	uniq := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := normalizeEnvKey(value)
		if normalized == "" {
			continue
		}
		uniq[normalized] = struct{}{}
	}

	keys := make([]string, 0, len(uniq))
	for key := range uniq {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func unsupportedEnvKeys(overrides map[string]string, allowlist []string, prefixAllowlist []string) []string {
	if len(overrides) == 0 {
		return nil
	}
	if len(allowlist) == 0 && len(prefixAllowlist) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(allowlist))
	for _, key := range allowlist {
		normalized := normalizeEnvKey(key)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}

	unsupported := make([]string, 0)
	for key := range overrides {
		normalized := normalizeEnvKey(key)
		if normalized == "" {
			continue
		}
		if isEnvKeyAllowed(normalized, allowed, prefixAllowlist) {
			continue
		}
		unsupported = append(unsupported, strings.TrimSpace(key))
	}
	sort.Strings(unsupported)
	return unsupported
}

func validateSensitiveEnvOverrideOptIn(overrides map[string]string, allowlist []string) error {
	if len(overrides) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(allowlist))
	for _, key := range allowlist {
		normalized := normalizeEnvKey(key)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}

	missingOptIn := make([]string, 0)
	for key := range overrides {
		normalized := normalizeEnvKey(key)
		if !shouldStripInheritedSensitiveEnv(normalized) {
			continue
		}
		if _, ok := allowed[normalized]; ok {
			continue
		}
		missingOptIn = append(missingOptIn, strings.TrimSpace(key))
	}

	if len(missingOptIn) == 0 {
		return nil
	}

	sort.Strings(missingOptIn)
	return fmt.Errorf(
		"sensitive env override keys require explicit AllowedEnvKeys opt-in: %s",
		strings.Join(missingOptIn, ", "),
	)
}

func validateEnvOverrideValues(overrides map[string]string) error {
	for key, value := range overrides {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		if unsafeEnvValuePattern.MatchString(value) {
			return fmt.Errorf("env override value for key %q contains unsafe characters", trimmedKey)
		}
	}
	return nil
}

func collectAllowedParentEnv(allowlist []string, prefixAllowlist []string) []string {
	if len(allowlist) == 0 && len(prefixAllowlist) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(allowlist))
	for _, key := range allowlist {
		normalized := normalizeEnvKey(key)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}
	if len(allowed) == 0 && len(prefixAllowlist) == 0 {
		return nil
	}

	entries := make([]string, 0, len(allowed))
	seenKeys := make(map[string]struct{}, len(allowed))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := normalizeEnvKey(key)
		if !isEnvKeyAllowed(normalized, allowed, prefixAllowlist) {
			continue
		}
		if _, exists := seenKeys[key]; exists {
			continue
		}
		entries = append(entries, fmt.Sprintf("%s=%s", key, value))
		seenKeys[key] = struct{}{}
	}
	sort.Strings(entries)
	return entries
}

func filterInheritedSensitiveRuntimeEnv(entries []string) ([]string, []string) {
	if len(entries) == 0 {
		return nil, nil
	}

	filtered := make([]string, 0, len(entries))
	stripped := make(map[string]struct{})
	for _, entry := range entries {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := normalizeEnvKey(key)
		if shouldStripInheritedSensitiveEnv(normalized) {
			stripped[normalized] = struct{}{}
			continue
		}
		filtered = append(filtered, entry)
	}

	return filtered, sortedSetKeys(stripped)
}

func shouldStripInheritedSensitiveEnv(normalizedKey string) bool {
	if normalizedKey == "" {
		return false
	}
	if _, ok := sensitiveInheritedEnvKeys[normalizedKey]; ok {
		return true
	}
	for _, prefix := range sensitiveInheritedEnvPrefixes {
		if strings.HasPrefix(normalizedKey, prefix) {
			return true
		}
	}
	return false
}

func disallowedInheritedSensitiveSelections(allowlist []string, prefixAllowlist []string) []string {
	disallowed := map[string]struct{}{}

	for _, key := range allowlist {
		normalized := normalizeEnvKey(key)
		if shouldStripInheritedSensitiveEnv(normalized) {
			disallowed[normalized] = struct{}{}
		}
	}

	for _, prefix := range prefixAllowlist {
		normalized := normalizeEnvKey(prefix)
		if normalized == "" {
			continue
		}
		if sensitiveInheritedPrefixOverlap(normalized) {
			disallowed[normalized+" (prefix)"] = struct{}{}
		}
	}

	return sortedSetKeys(disallowed)
}

func sensitiveInheritedPrefixOverlap(normalizedPrefix string) bool {
	if normalizedPrefix == "" {
		return false
	}

	for key := range sensitiveInheritedEnvKeys {
		if strings.HasPrefix(key, normalizedPrefix) || strings.HasPrefix(normalizedPrefix, key) {
			return true
		}
	}
	for _, sensitivePrefix := range sensitiveInheritedEnvPrefixes {
		if strings.HasPrefix(sensitivePrefix, normalizedPrefix) || strings.HasPrefix(normalizedPrefix, sensitivePrefix) {
			return true
		}
	}
	return false
}

func buildCommandEnv(base []string, allowlist []string, prefixAllowlist []string, overrides map[string]string, useSafePath bool, trustedPath string) ([]string, []string) {
	filteredBase, _ := filterInheritedSensitiveRuntimeEnv(base)

	allowed := make(map[string]struct{}, len(allowlist))
	for _, key := range allowlist {
		normalized := normalizeEnvKey(key)
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}

	merged := make(map[string]string, len(allowlist)+len(overrides)+1)
	mergedKeyByNormalized := make(map[string]string, len(allowlist)+len(overrides)+1)
	blocked := map[string]struct{}{}

	setMerged := func(key string, value string) {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return
		}
		normalized := normalizeEnvKey(trimmedKey)
		if normalized == "" {
			return
		}
		if existingKey, ok := mergedKeyByNormalized[normalized]; ok && existingKey != trimmedKey {
			delete(merged, existingKey)
		}
		mergedKeyByNormalized[normalized] = trimmedKey
		merged[trimmedKey] = value
	}

	for _, entry := range filteredBase {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := normalizeEnvKey(key)
		if len(allowed) > 0 || len(prefixAllowlist) > 0 {
			if !isEnvKeyAllowed(normalized, allowed, prefixAllowlist) {
				continue
			}
		}
		if useSafePath && normalized == "PATH" {
			continue
		}
		if isBlockedEnvKey(key) {
			blocked[normalized] = struct{}{}
			continue
		}
		setMerged(key, value)
	}

	for key, value := range overrides {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		normalized := normalizeEnvKey(trimmedKey)
		if len(allowed) > 0 || len(prefixAllowlist) > 0 {
			if !isEnvKeyAllowed(normalized, allowed, prefixAllowlist) {
				continue
			}
		}
		if useSafePath && normalized == "PATH" {
			continue
		}
		if isBlockedEnvKey(trimmedKey) {
			blocked[normalized] = struct{}{}
			continue
		}
		setMerged(trimmedKey, value)
	}

	pathValue := strings.TrimSpace(trustedPath)
	if useSafePath {
		if pathValue == "" {
			pathValue = normalizedExecutablePath
		}
	} else {
		pathValue = ""
		if pathKey, ok := mergedKeyByNormalized["PATH"]; ok {
			pathValue = strings.TrimSpace(merged[pathKey])
		}
		if pathValue == "" {
			pathValue = normalizedExecutablePath
		}
	}
	setMerged("PATH", pathValue)

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, fmt.Sprintf("%s=%s", key, merged[key]))
	}

	return result, sortedSetKeys(blocked)
}

func sortedSetKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isBlockedEnvKey(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return false
	}

	normalized := strings.ToUpper(trimmed)
	if _, ok := blockedEnvKeys[normalized]; ok {
		return true
	}

	for _, prefix := range blockedEnvPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}

	return false
}

func normalizeEnvKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

func isEnvKeyAllowed(normalizedKey string, allowed map[string]struct{}, prefixAllowlist []string) bool {
	if normalizedKey == "" {
		return false
	}
	if len(allowed) == 0 && len(prefixAllowlist) == 0 {
		return true
	}
	if _, ok := allowed[normalizedKey]; ok {
		return true
	}
	for _, prefix := range prefixAllowlist {
		if strings.HasPrefix(normalizedKey, prefix) {
			return true
		}
	}
	return false
}

func isSensitiveOption(flag string) bool {
	normalized := strings.TrimSpace(flag)
	normalized = strings.TrimPrefix(normalized, "--")
	normalized = strings.TrimPrefix(normalized, "-")
	normalized = strings.TrimSuffix(normalized, "=")
	return isSensitiveKey(normalized)
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(strings.TrimLeft(key, "-")))
	if normalized == "" {
		return false
	}

	if sensitiveKeyNamePattern.MatchString(normalized) {
		return true
	}

	if strings.HasPrefix(normalized, "AUTH") {
		return true
	}

	if strings.Contains(normalized, "TOKEN") {
		return true
	}
	if strings.Contains(normalized, "PASSWORD") || strings.Contains(normalized, "PASSWD") {
		return true
	}
	if strings.Contains(normalized, "PASS") {
		return true
	}
	if strings.Contains(normalized, "SECRET") {
		return true
	}
	if strings.Contains(normalized, "COOKIE") {
		return true
	}
	if strings.Contains(normalized, "SESSION") {
		return true
	}
	if strings.Contains(normalized, "CREDENTIAL") {
		return true
	}
	if strings.Contains(normalized, "API_KEY") || strings.Contains(normalized, "APIKEY") {
		return true
	}
	if strings.Contains(normalized, "PRIVATE_KEY") {
		return true
	}
	if strings.Contains(normalized, "_KEY") || strings.HasSuffix(normalized, "KEY") {
		return true
	}

	return false
}

func redactSensitiveText(value string) string {
	redacted := strings.TrimSpace(value)
	if redacted == "" {
		return ""
	}

	redacted = proxyLinkPattern.ReplaceAllString(redacted, "[redacted-proxy-link]")
	redacted = bearerTokenPattern.ReplaceAllString(redacted, "$1 [redacted]")
	redacted = authorizationHeaderPattern.ReplaceAllString(redacted, "$1: [redacted]")
	redacted = cookieHeaderPattern.ReplaceAllString(redacted, "$1: [redacted]")
	redacted = setCookieHeaderPattern.ReplaceAllString(redacted, "$1: [redacted]")
	redacted = sensitiveAssignmentPattern.ReplaceAllString(redacted, "$1$2[redacted]")
	redacted = sensitiveOptionPattern.ReplaceAllString(redacted, "$1$2[redacted]")
	redacted = sensitiveHeaderPattern.ReplaceAllString(redacted, "$1: [redacted]")
	redacted = sensitiveJSONPattern.ReplaceAllString(redacted, "\"$1\":\"[redacted]\"")
	redacted = sensitiveQueryPattern.ReplaceAllString(redacted, "$1=[redacted]")
	return redacted
}

func fallbackLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
