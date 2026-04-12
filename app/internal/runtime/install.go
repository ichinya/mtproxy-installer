package runtime

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultInstallDir = "/opt/mtproxy-installer"
	InstallDirEnvKey  = "INSTALL_DIR"
)

type LoadOptions struct {
	InstallDir string
	Logger     *slog.Logger
}

type RuntimeInstallation struct {
	InstallDir  string
	Paths       RuntimePaths
	Env         *EnvFile
	Provider    ProviderDescriptor
	Permissions PermissionSummary
}

type RuntimePaths struct {
	InstallDir   string
	EnvFile      string
	ComposeFile  string
	TelemtConfig string
	MTGConfig    string
}

type PermissionSummary struct {
	InstallDir     PermissionCheck
	EnvFile        PermissionCheck
	ComposeFile    PermissionCheck
	ProviderConfig PermissionCheck
}

type PermissionCheck struct {
	Path      string
	Exists    bool
	Readable  bool
	Writable  bool
	ReadNote  string
	WriteNote string
}

func Load(options LoadOptions) (*RuntimeInstallation, error) {
	logger := fallbackLogger(options.Logger)

	installDir := resolveInstallDir(options, logger)
	paths := RuntimePaths{
		InstallDir:   installDir,
		EnvFile:      filepath.Join(installDir, ".env"),
		ComposeFile:  filepath.Join(installDir, "docker-compose.yml"),
		TelemtConfig: filepath.Join(installDir, "providers", string(ProviderTelemt), "telemt.toml"),
		MTGConfig:    filepath.Join(installDir, "providers", string(ProviderMTG), "mtg.conf"),
	}

	hardenedPaths, err := hardenRuntimeLoadPaths(paths, logger)
	if err != nil {
		return nil, err
	}
	paths = hardenedPaths

	if err := requireInstallDir(paths.InstallDir, logger); err != nil {
		return nil, err
	}
	if err := requireFile(paths.EnvFile, "runtime env file", logger); err != nil {
		return nil, err
	}
	if err := requireFile(paths.ComposeFile, "runtime compose file", logger); err != nil {
		return nil, err
	}

	envFile, err := LoadEnv(paths.EnvFile, logger)
	if err != nil {
		return nil, err
	}

	provider, err := DetectProvider(paths.InstallDir, envFile, logger)
	if err != nil {
		return nil, err
	}

	permissions := PermissionSummary{
		InstallDir:     probePermissions(paths.InstallDir),
		EnvFile:        probePermissions(paths.EnvFile),
		ComposeFile:    probePermissions(paths.ComposeFile),
		ProviderConfig: probePermissions(provider.ConfigPath),
	}

	logPermissionProbe(logger, "install_dir", permissions.InstallDir)
	logPermissionProbe(logger, "env_file", permissions.EnvFile)
	logPermissionProbe(logger, "compose_file", permissions.ComposeFile)
	logPermissionProbe(logger, "provider_config", permissions.ProviderConfig)

	if err := ensureReadable("install directory", permissions.InstallDir, logger); err != nil {
		return nil, err
	}
	if err := ensureReadable("runtime env file", permissions.EnvFile, logger); err != nil {
		return nil, err
	}
	if err := ensureReadable("runtime compose file", permissions.ComposeFile, logger); err != nil {
		return nil, err
	}
	if err := ensureReadable("provider config file", permissions.ProviderConfig, logger); err != nil {
		return nil, err
	}

	runtime := &RuntimeInstallation{
		InstallDir:  paths.InstallDir,
		Paths:       paths,
		Env:         envFile,
		Provider:    provider,
		Permissions: permissions,
	}

	logger.Info(
		"runtime discovery resolved",
		"install_dir", runtime.InstallDir,
		"provider", runtime.Provider.Name,
		"provider_source", runtime.Provider.Source,
		"provider_config", runtime.Provider.ConfigPath,
		"compose_file", runtime.Paths.ComposeFile,
		"env_file", runtime.Paths.EnvFile,
		"install_dir_writable", runtime.Permissions.InstallDir.Writable,
		"env_writable", runtime.Permissions.EnvFile.Writable,
		"compose_writable", runtime.Permissions.ComposeFile.Writable,
		"provider_config_writable", runtime.Permissions.ProviderConfig.Writable,
	)

	return runtime, nil
}

func resolveInstallDir(options LoadOptions, logger *slog.Logger) string {
	logger.Debug("install-dir resolution start")

	if candidate := strings.TrimSpace(options.InstallDir); candidate != "" {
		logger.Debug("install-dir selected from explicit option", "install_dir", candidate)
		return normalizeInstallDir(candidate, logger)
	}

	if candidate := strings.TrimSpace(os.Getenv(InstallDirEnvKey)); candidate != "" {
		logger.Debug("install-dir selected from environment", "env_key", InstallDirEnvKey, "install_dir", candidate)
		return normalizeInstallDir(candidate, logger)
	}

	logger.Debug("install-dir fallback to default", "install_dir", DefaultInstallDir)
	return normalizeInstallDir(DefaultInstallDir, logger)
}

func normalizeInstallDir(path string, logger *slog.Logger) string {
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		logger.Debug("install-dir normalized", "install_dir", clean, "mode", "absolute")
		return clean
	}

	abs, err := filepath.Abs(clean)
	if err != nil {
		logger.Debug("install-dir absolute normalization failed", "candidate", clean, "error", err.Error())
		return clean
	}
	logger.Debug("install-dir normalized", "install_dir", abs, "mode", "relative-to-absolute")
	return abs
}

func requireInstallDir(path string, logger *slog.Logger) error {
	logger.Debug("install-dir probe", "path", path)
	info, err := os.Stat(path)
	if err != nil {
		code := CodeInstallDirMissing
		message := "install dir missing"
		if errors.Is(err, os.ErrPermission) {
			code = CodePermissionDenied
			message = "permission denied while accessing install directory"
		} else if !errors.Is(err, os.ErrNotExist) {
			code = CodeInstallDirInvalid
			message = "failed to stat install directory"
		}
		runtimeErr := &RuntimeError{
			Code:    code,
			Path:    path,
			Message: message,
			Err:     err,
		}
		logger.Error("install-dir probe failed", "path", path, "error", runtimeErr.Error())
		return runtimeErr
	}

	if !info.IsDir() {
		runtimeErr := &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    path,
			Message: "install directory path is not a directory",
		}
		logger.Error("install-dir probe failed", "path", path, "error", runtimeErr.Error())
		return runtimeErr
	}

	dirEntries, err := os.ReadDir(path)
	if err != nil {
		runtimeErr := &RuntimeError{
			Code:    CodeInstallDirUnreadable,
			Path:    path,
			Message: "install directory is not readable",
			Err:     err,
		}
		logger.Error("install-dir probe failed", "path", path, "error", runtimeErr.Error())
		return runtimeErr
	}

	logger.Debug("install-dir probe success", "path", path, "entries", len(dirEntries))
	return nil
}

func requireFile(path string, fileType string, logger *slog.Logger) error {
	logger.Debug("runtime file probe", "path", path, "file_type", fileType)
	info, err := os.Stat(path)
	if err != nil {
		code := CodeRequiredFileMissing
		message := fmt.Sprintf("%s is missing", fileType)
		if errors.Is(err, os.ErrPermission) {
			code = CodePermissionDenied
			message = fmt.Sprintf("permission denied while probing %s", fileType)
		}
		runtimeErr := &RuntimeError{
			Code:    code,
			Path:    path,
			Message: message,
			Err:     err,
		}
		logger.Error("required runtime file missing", "path", path, "file_type", fileType, "error", runtimeErr.Error())
		return runtimeErr
	}

	if info.IsDir() {
		runtimeErr := &RuntimeError{
			Code:    CodeRequiredFileMissing,
			Path:    path,
			Message: fmt.Sprintf("%s path points to a directory", fileType),
		}
		logger.Error("required runtime file invalid", "path", path, "file_type", fileType, "error", runtimeErr.Error())
		return runtimeErr
	}

	logger.Debug("runtime file probe success", "path", path, "file_type", fileType)
	return nil
}

func probePermissions(path string) PermissionCheck {
	check := PermissionCheck{
		Path: path,
	}

	info, err := os.Stat(path)
	if err != nil {
		check.Exists = false
		check.Readable = false
		check.ReadNote = err.Error()
		check.Writable, check.WriteNote = probeParentWritable(path)
		return check
	}

	check.Exists = true
	check.Readable, check.ReadNote = probeReadable(path, info)
	check.Writable, check.WriteNote = probeWritable(path, info)
	return check
}

func probeReadable(path string, info os.FileInfo) (bool, string) {
	if info.IsDir() {
		_, err := os.ReadDir(path)
		if err != nil {
			return false, err.Error()
		}
		return true, "directory listing is readable"
	}

	file, err := os.Open(path)
	if err != nil {
		return false, err.Error()
	}
	file.Close()
	return true, "file open for read succeeded"
}

func probeWritable(path string, info os.FileInfo) (bool, string) {
	if info.IsDir() {
		if modeAllowsWrite(info.Mode()) {
			return true, "directory mode indicates write access"
		}
		return false, "directory mode does not indicate write access"
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		file.Close()
		return true, "file open for write succeeded"
	}
	return false, err.Error()
}

func probeParentWritable(path string) (bool, string) {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return false, fmt.Sprintf("cannot stat parent directory %s: %v", parent, err)
	}
	if !info.IsDir() {
		return false, fmt.Sprintf("parent path %s is not a directory", parent)
	}
	if modeAllowsWrite(info.Mode()) {
		return true, fmt.Sprintf("parent directory %s mode indicates write access", parent)
	}
	return false, fmt.Sprintf("parent directory %s mode does not indicate write access", parent)
}

func modeAllowsWrite(mode os.FileMode) bool {
	return mode&0200 != 0 || mode&0020 != 0 || mode&0002 != 0
}

func ensureReadable(resource string, check PermissionCheck, logger *slog.Logger) error {
	if check.Readable {
		return nil
	}

	runtimeErr := &RuntimeError{
		Code:    CodePermissionDenied,
		Path:    check.Path,
		Message: fmt.Sprintf("%s is not readable", resource),
	}
	logger.Error(
		"runtime permission precheck failed",
		"resource", resource,
		"path", check.Path,
		"note", check.ReadNote,
		"error", runtimeErr.Error(),
	)
	return runtimeErr
}

func logPermissionProbe(logger *slog.Logger, resource string, check PermissionCheck) {
	logger.Debug(
		"runtime permission precheck",
		"resource", resource,
		"path", check.Path,
		"exists", check.Exists,
		"readable", check.Readable,
		"writable", check.Writable,
		"read_note", check.ReadNote,
		"write_note", check.WriteNote,
	)
}

func fallbackLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func hardenRuntimeLoadPaths(paths RuntimePaths, logger *slog.Logger) (RuntimePaths, error) {
	resolvedInstallDir, err := resolveAbsoluteRuntimeLoadPath(paths.InstallDir)
	if err != nil {
		runtimeErr := &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    paths.InstallDir,
			Message: "invalid install directory path",
			Err:     err,
		}
		logger.Error("runtime path hardening failed", "path", paths.InstallDir, "error", runtimeErr.Error())
		return RuntimePaths{}, runtimeErr
	}

	if err := validatePathChainNoSymlinksForRuntime(resolvedInstallDir); err != nil {
		runtimeErr := &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    resolvedInstallDir,
			Message: "install directory path chain is unsafe",
			Err:     err,
		}
		logger.Error("runtime path hardening failed", "path", resolvedInstallDir, "error", runtimeErr.Error())
		return RuntimePaths{}, runtimeErr
	}
	if err := ensurePathNotSymlinkForRuntime(resolvedInstallDir, "install directory"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Debug(
				"runtime path hardening skipped install-dir symlink check for missing path",
				"path", resolvedInstallDir,
			)
		} else {
			runtimeErr := &RuntimeError{
				Code:    CodeInstallDirInvalid,
				Path:    resolvedInstallDir,
				Message: "install directory must not be a symlink",
				Err:     err,
			}
			logger.Error("runtime path hardening failed", "path", resolvedInstallDir, "error", runtimeErr.Error())
			return RuntimePaths{}, runtimeErr
		}
	}

	envFile, err := hardenRuntimeLoadFilePath(resolvedInstallDir, paths.EnvFile, "runtime env file")
	if err != nil {
		logger.Error("runtime path hardening failed", "path", paths.EnvFile, "error", err.Error())
		return RuntimePaths{}, err
	}
	composeFile, err := hardenRuntimeLoadFilePath(resolvedInstallDir, paths.ComposeFile, "runtime compose file")
	if err != nil {
		logger.Error("runtime path hardening failed", "path", paths.ComposeFile, "error", err.Error())
		return RuntimePaths{}, err
	}
	telemtConfig, err := hardenRuntimeLoadFilePath(
		resolvedInstallDir,
		paths.TelemtConfig,
		"telemt provider config file",
	)
	if err != nil {
		logger.Error("runtime path hardening failed", "path", paths.TelemtConfig, "error", err.Error())
		return RuntimePaths{}, err
	}
	mtgConfig, err := hardenRuntimeLoadFilePath(
		resolvedInstallDir,
		paths.MTGConfig,
		"mtg provider config file",
	)
	if err != nil {
		logger.Error("runtime path hardening failed", "path", paths.MTGConfig, "error", err.Error())
		return RuntimePaths{}, err
	}

	logger.Debug(
		"runtime path hardening finished",
		"install_dir", resolvedInstallDir,
		"env_file", envFile,
		"compose_file", composeFile,
		"telemt_config", telemtConfig,
		"mtg_config", mtgConfig,
	)

	return RuntimePaths{
		InstallDir:   resolvedInstallDir,
		EnvFile:      envFile,
		ComposeFile:  composeFile,
		TelemtConfig: telemtConfig,
		MTGConfig:    mtgConfig,
	}, nil
}

func hardenRuntimeLoadFilePath(installDir string, path string, label string) (string, error) {
	resolvedPath, err := resolveAbsoluteRuntimeLoadPath(path)
	if err != nil {
		return "", &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    path,
			Message: fmt.Sprintf("invalid %s path", label),
			Err:     err,
		}
	}

	if !isPathWithinRuntimeInstallDir(installDir, resolvedPath) {
		return "", &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    resolvedPath,
			Message: fmt.Sprintf("%s path escapes install directory", label),
		}
	}
	if err := validatePathChainNoSymlinksForRuntime(resolvedPath); err != nil {
		return "", &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    resolvedPath,
			Message: fmt.Sprintf("%s path chain is unsafe", label),
			Err:     err,
		}
	}

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolvedPath, nil
		}
		code := CodeInstallDirInvalid
		message := fmt.Sprintf("%s path is not accessible", label)
		if errors.Is(err, os.ErrPermission) {
			code = CodePermissionDenied
			message = fmt.Sprintf("permission denied while hardening %s", label)
		}
		return "", &RuntimeError{
			Code:    code,
			Path:    resolvedPath,
			Message: message,
			Err:     err,
		}
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return "", &RuntimeError{
			Code:    CodeInstallDirInvalid,
			Path:    resolvedPath,
			Message: fmt.Sprintf("%s must not be a symlink", label),
		}
	}

	return resolvedPath, nil
}

func resolveAbsoluteRuntimeLoadPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func validatePathChainNoSymlinksForRuntime(path string) error {
	resolved, err := resolveAbsoluteRuntimeLoadPath(path)
	if err != nil {
		return err
	}

	volume := filepath.VolumeName(resolved)
	segments := splitPathSegmentsForRuntime(resolved, volume)
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

func splitPathSegmentsForRuntime(path string, volume string) []string {
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

func ensurePathNotSymlinkForRuntime(path string, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s %q is not accessible: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	return nil
}

func isPathWithinRuntimeInstallDir(base string, target string) bool {
	baseResolved, err := resolveAbsoluteRuntimeLoadPath(base)
	if err != nil {
		return false
	}
	targetResolved, err := resolveAbsoluteRuntimeLoadPath(target)
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
