package pathutil

import (
	"errors"
	"path/filepath"
	"strings"
)

// IsPOSIXRootedPath keeps project paths like "/opt/mtproxy-installer" stable
// even when the CLI is executed on a Windows host.
func IsPOSIXRootedPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	if filepath.VolumeName(trimmed) != "" {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(trimmed), "/")
}

func CleanPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if IsPOSIXRootedPath(trimmed) {
		return filepath.ToSlash(cleaned)
	}
	return cleaned
}

func ResolvePath(path string) (string, error) {
	cleaned := CleanPath(path)
	if cleaned == "" {
		return "", errors.New("path is required")
	}
	if IsPOSIXRootedPath(cleaned) {
		return cleaned, nil
	}
	absolute, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func CanonicalPathKey(path string) string {
	cleaned := CleanPath(path)
	if cleaned == "" {
		return ""
	}
	normalized := filepath.ToSlash(cleaned)
	if filepath.VolumeName(cleaned) != "" {
		return strings.ToLower(normalized)
	}
	return normalized
}
