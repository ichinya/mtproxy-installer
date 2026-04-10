package runtime

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	envProviderKey          = "PROVIDER"
	envPortKey              = "PORT"
	envAPIPortKey           = "API_PORT"
	envTelemtImageKey       = "TELEMT_IMAGE"
	envTelemtImageSourceKey = "TELEMT_IMAGE_SOURCE"
	envMtgImageKey          = "MTG_IMAGE"
	envMtgImageSourceKey    = "MTG_IMAGE_SOURCE"
)

var (
	envKeyPattern       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretKeyPattern    = regexp.MustCompile(`(?i)(secret|token|password|passphrase|private_key|api_key|apikey)`)
	proxyLinkEnvPattern = regexp.MustCompile(`(?i)tg://proxy\?[^\s]+|https://t\.me/proxy\?\S+`)
)

type EnvFile struct {
	Path   string
	values map[string]string
}

func LoadEnv(path string, logger *slog.Logger) (*EnvFile, error) {
	logger = fallbackLogger(logger)

	logger.Debug("env file resolution start", "path", path)
	file, err := os.Open(path)
	if err != nil {
		code := CodeRequiredFileMissing
		message := "unable to open runtime .env file"
		if os.IsPermission(err) {
			code = CodePermissionDenied
			message = "permission denied while opening runtime .env file"
		}
		runtimeErr := &RuntimeError{
			Code:    code,
			Path:    path,
			Message: message,
			Err:     err,
		}
		logger.Error("env file open failed", "path", path, "error", runtimeErr.Error())
		return nil, runtimeErr
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNumber := 0

	logger.Debug("env parsing start", "path", path)
	for scanner.Scan() {
		lineNumber++
		raw := scanner.Text()
		key, value, skip, parseErr := parseEnvLine(raw)
		if parseErr != nil {
			runtimeErr := &RuntimeError{
				Code:    CodeEnvParse,
				Path:    path,
				Field:   fmt.Sprintf("line:%d", lineNumber),
				Message: parseErr.Error(),
				Err:     parseErr,
			}
			logger.Error("env parsing failed", "path", path, "line", lineNumber, "error", runtimeErr.Error())
			return nil, runtimeErr
		}
		if skip {
			logger.Debug("env line ignored", "path", path, "line", lineNumber)
			continue
		}

		if previous, ok := values[key]; ok {
			logger.Debug(
				"env key overwritten",
				"path", path,
				"line", lineNumber,
				"key", key,
				"previous", redactEnvValue(key, previous),
				"next", redactEnvValue(key, value),
			)
		}
		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		runtimeErr := &RuntimeError{
			Code:    CodeEnvParse,
			Path:    path,
			Message: "scanner failed while reading runtime .env file",
			Err:     err,
		}
		logger.Error("env scan failed", "path", path, "error", runtimeErr.Error())
		return nil, runtimeErr
	}

	env := &EnvFile{
		Path:   path,
		values: values,
	}

	logger.Debug("env parsing finish", "path", path, "entries", len(values))
	logger.Info(
		"runtime env loaded",
		"path", path,
		"entries", len(values),
		"provider", env.ProviderValue(),
		"snapshot", env.SafeSnapshot(),
	)

	return env, nil
}

func parseEnvLine(raw string) (key string, value string, skip bool, err error) {
	trimmed := strings.TrimSpace(strings.TrimRight(raw, "\r"))
	if trimmed == "" {
		return "", "", true, nil
	}
	if strings.HasPrefix(trimmed, "#") {
		return "", "", true, nil
	}
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}

	eqPos := strings.Index(trimmed, "=")
	if eqPos <= 0 {
		return "", "", false, fmt.Errorf("malformed env line %q: expected KEY=VALUE", trimmed)
	}

	key = strings.TrimSpace(trimmed[:eqPos])
	value = strings.TrimSpace(trimmed[eqPos+1:])

	if !envKeyPattern.MatchString(key) {
		return "", "", false, fmt.Errorf("malformed env key %q", key)
	}

	return key, unquoteEnvValue(value), false, nil
}

func unquoteEnvValue(value string) string {
	if len(value) < 2 {
		return value
	}
	first := value[0]
	last := value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

func (e *EnvFile) ProviderValue() string {
	if e == nil {
		return ""
	}
	raw, ok := e.values[envProviderKey]
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func (e *EnvFile) Value(key string) (string, bool) {
	if e == nil {
		return "", false
	}
	value, ok := e.values[key]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func (e *EnvFile) Port() (int, bool, error) {
	return e.intValue(envPortKey)
}

func (e *EnvFile) APIPort() (int, bool, error) {
	return e.intValue(envAPIPortKey)
}

func (e *EnvFile) TelemtImage() string {
	return e.stringValue(envTelemtImageKey)
}

func (e *EnvFile) TelemtImageSource() string {
	return e.stringValue(envTelemtImageSourceKey)
}

func (e *EnvFile) MTGImage() string {
	return e.stringValue(envMtgImageKey)
}

func (e *EnvFile) MTGImageSource() string {
	return e.stringValue(envMtgImageSourceKey)
}

func (e *EnvFile) SafeSnapshot() map[string]string {
	snapshot := make(map[string]string)
	if e == nil {
		return snapshot
	}

	for _, key := range e.Keys() {
		value := e.values[key]
		snapshot[key] = redactEnvValue(key, value)
	}
	return snapshot
}

func (e *EnvFile) Keys() []string {
	if e == nil {
		return nil
	}
	keys := make([]string, 0, len(e.values))
	for key := range e.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (e *EnvFile) stringValue(key string) string {
	if e == nil {
		return ""
	}
	value, ok := e.values[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (e *EnvFile) intValue(key string) (int, bool, error) {
	value := e.stringValue(key)
	if value == "" {
		return 0, false, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, true, &RuntimeError{
			Code:    CodeEnvParse,
			Path:    e.Path,
			Field:   key,
			Message: fmt.Sprintf("invalid integer value for %s", key),
			Err:     err,
		}
	}
	return parsed, true, nil
}

func redactEnvValue(key string, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	if secretKeyPattern.MatchString(key) {
		return "[redacted]"
	}
	if proxyLinkEnvPattern.MatchString(trimmed) {
		return "[redacted-proxy-link]"
	}
	return trimmed
}
