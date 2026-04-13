package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"

	execadapter "mtproxy-installer/app/internal/exec"
	"mtproxy-installer/app/internal/output"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/scripts"
)

const lifecycleScriptsRootEnv = "MTPROXY_SCRIPTS_ROOT"

var cliTrustBoundaryEnvKeyAllowlist = map[string]struct{}{
	"HOME":             {},
	"XDG_RUNTIME_DIR":  {},
	"HTTP_PROXY":       {},
	"HTTPS_PROXY":      {},
	"NO_PROXY":         {},
	"DOCKER_HOST":      {},
	"DOCKER_CERT_PATH": {},
}

var cliTrustBoundaryEnvPrefixAllowlist = []string{
	"DOCKER_",
	"COMPOSE_",
}

type lifecycleScriptsManager interface {
	Install(context.Context, scripts.InstallOptions) (execadapter.Result, error)
	Update(context.Context, scripts.UpdateOptions) (execadapter.Result, error)
	Uninstall(context.Context, scripts.UninstallOptions) (execadapter.Result, error)
}

var newLifecycleScriptsManager = func(logger *slog.Logger) (lifecycleScriptsManager, error) {
	repoRootOverride := strings.TrimSpace(os.Getenv(lifecycleScriptsRootEnv))
	return scripts.NewManager(scripts.ManagerOptions{
		Logger:          logger,
		RepoRoot:        repoRootOverride,
		RepoRootFromEnv: repoRootOverride != "",
	})
}

type installCommandOptions struct {
	provider                  string
	port                      int
	installDir                string
	allowNonDefaultInstallDir bool
	apiPort                   int
	tlsDomain                 string
	publicIP                  string
	secret                    string
	proxyUser                 string
	telemtImage               string
	telemtImageSource         string
	mtgImage                  string
	mtgImageSource            string
	extraEnv                  map[string]string
	allowTrustBoundaryEnv     bool
}

func runInstall(ctx commandContext) error {
	ctx.Logger.Info("install command entry", "args_count", len(ctx.Args))

	options, err := parseInstallCommandArgs(ctx.Args)
	if err != nil {
		ctx.Logger.Error("install command argument parse failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	envPreview := buildInstallEnvPreview(options)
	ctx.Logger.Debug(
		"install command parsed options",
		"provider", options.provider,
		"port", options.port,
		"install_dir", options.installDir,
		"allow_non_default_install_dir", options.allowNonDefaultInstallDir,
		"api_port", options.apiPort,
		"tls_domain", normalizeLogOptional(options.tlsDomain),
		"public_ip", normalizeLogOptional(options.publicIP),
		"secret_provided", strings.TrimSpace(options.secret) != "",
		"proxy_user", normalizeLogOptional(options.proxyUser),
		"telemt_image", normalizeLogOptional(options.telemtImage),
		"telemt_image_source", normalizeLogOptional(options.telemtImageSource),
		"mtg_image", normalizeLogOptional(options.mtgImage),
		"mtg_image_source", normalizeLogOptional(options.mtgImageSource),
		"mapped_env_override_keys", sortedMapKeys(envPreview),
		"mapped_env_overrides", execadapter.RedactEnvSnapshot(envPreview),
		"allow_trust_boundary_env", options.allowTrustBoundaryEnv,
	)

	manager, err := newLifecycleScriptsManager(ctx.Logger)
	if err != nil {
		ctx.Logger.Error("install scripts manager init failed", "error", redactForCommand(ctx.Command, err.Error()))
		return err
	}

	ctx.Logger.Info(
		"install lifecycle begin",
		"provider", options.provider,
		"port", options.port,
		"install_dir", options.installDir,
		"allow_non_default_install_dir", options.allowNonDefaultInstallDir,
		"allow_trust_boundary_env", options.allowTrustBoundaryEnv,
	)

	result, runErr := manager.Install(context.Background(), scripts.InstallOptions{
		Provider:                  runtime.Provider(options.provider),
		Port:                      options.port,
		InstallDir:                options.installDir,
		AllowNonDefaultInstallDir: options.allowNonDefaultInstallDir,
		APIPort:                   options.apiPort,
		TLSDomain:                 options.tlsDomain,
		PublicIP:                  options.publicIP,
		Secret:                    options.secret,
		ProxyUser:                 options.proxyUser,
		TelemtImage:               options.telemtImage,
		TelemtImageSource:         options.telemtImageSource,
		MTGImage:                  options.mtgImage,
		MTGImageSource:            options.mtgImageSource,
		ExtraEnv:                  options.extraEnv,
		AllowTrustBoundaryEnv:     options.allowTrustBoundaryEnv,
	})
	if runErr != nil {
		ctx.Logger.Error(
			"install lifecycle failed",
			"provider", options.provider,
			"install_dir", options.installDir,
			"exit_status", result.ExitCode,
			"stderr_summary", execadapter.RedactText(result.StderrSummary),
			"error", redactForCommand(ctx.Command, runErr.Error()),
		)
		return runErr
	}

	summary := scripts.ParseInstallLifecycle(result)
	ctx.Logger.Debug(
		"install lifecycle parsed summary",
		"provider", summary.Provider,
		"install_dir", summary.InstallDir,
		"public_endpoint", normalizeLogOptional(summary.PublicEndpoint),
		"api_endpoint", normalizeLogOptional(summary.APIEndpoint),
		"config_path", normalizeLogOptional(summary.ConfigPath),
		"proxy_link_present", summary.ProxyLinkPresent,
		"sensitive_output_present", summary.SensitiveOutputPresent,
		"parse_diagnostics", summary.ParseDiagnostics,
	)

	if err := writeCommandOutput(ctx.Stdout, output.RenderInstallLifecycle(summary)); err != nil {
		return err
	}

	ctx.Logger.Info(
		"install lifecycle finish",
		"provider", summary.Provider,
		"install_dir", summary.InstallDir,
		"public_endpoint", normalizeLogOptional(summary.PublicEndpoint),
		"api_endpoint", normalizeLogOptional(summary.APIEndpoint),
		"config_path", normalizeLogOptional(summary.ConfigPath),
		"proxy_link_present", summary.ProxyLinkPresent,
		"sensitive_output_present", summary.SensitiveOutputPresent,
	)

	return nil
}

func parseInstallCommandArgs(args []string) (installCommandOptions, error) {
	envFlag := newEnvOverrideFlag("install")
	options := installCommandOptions{
		provider:   string(runtime.ProviderTelemt),
		port:       443,
		installDir: runtime.DefaultInstallDir,
	}

	flagSet := flag.NewFlagSet("install", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.StringVar(&options.provider, "provider", options.provider, "runtime provider")
	flagSet.IntVar(&options.port, "port", options.port, "public proxy port")
	flagSet.StringVar(&options.installDir, "install-dir", options.installDir, "runtime install dir")
	flagSet.BoolVar(&options.allowNonDefaultInstallDir, "allow-non-default-install-dir", false, "allow non-default install dir")
	flagSet.IntVar(&options.apiPort, "api-port", 0, "telemt API port override")
	flagSet.StringVar(&options.tlsDomain, "tls-domain", "", "TLS domain override")
	flagSet.StringVar(&options.publicIP, "public-ip", "", "public IP override")
	flagSet.StringVar(&options.secret, "secret", "", "proxy secret override")
	flagSet.StringVar(&options.proxyUser, "proxy-user", "", "telemt proxy user override")
	flagSet.StringVar(&options.telemtImage, "telemt-image", "", "telemt image override")
	flagSet.StringVar(&options.telemtImageSource, "telemt-image-source", "", "telemt image source override")
	flagSet.StringVar(&options.mtgImage, "mtg-image", "", "mtg image override")
	flagSet.StringVar(&options.mtgImageSource, "mtg-image-source", "", "mtg image source override")
	flagSet.BoolVar(&options.allowTrustBoundaryEnv, "allow-trust-boundary-env", false, "allow trust-boundary --env overrides")
	flagSet.Var(envFlag, "env", "extra env override in KEY=VALUE form (repeatable)")

	if err := flagSet.Parse(args); err != nil {
		return installCommandOptions{}, fmt.Errorf("install command flag parse failed: %w", err)
	}

	if flagSet.NArg() > 0 {
		return installCommandOptions{}, fmt.Errorf("install command does not accept positional arguments: %s", strings.Join(flagSet.Args(), " "))
	}

	options.provider = strings.ToLower(strings.TrimSpace(options.provider))
	switch runtime.Provider(options.provider) {
	case runtime.ProviderTelemt, runtime.ProviderMTG:
	default:
		return installCommandOptions{}, fmt.Errorf("install command provider %q is unsupported", options.provider)
	}

	if options.port < 1 || options.port > 65535 {
		return installCommandOptions{}, fmt.Errorf("install command --port value %d is invalid", options.port)
	}
	if options.apiPort < 0 || options.apiPort > 65535 {
		return installCommandOptions{}, fmt.Errorf("install command --api-port value %d is invalid", options.apiPort)
	}

	options.installDir = strings.TrimSpace(options.installDir)
	if options.installDir == "" {
		options.installDir = runtime.DefaultInstallDir
	}
	options.tlsDomain = strings.TrimSpace(options.tlsDomain)
	options.publicIP = strings.TrimSpace(options.publicIP)
	options.secret = strings.TrimSpace(options.secret)
	options.proxyUser = strings.TrimSpace(options.proxyUser)
	options.telemtImage = strings.TrimSpace(options.telemtImage)
	options.telemtImageSource = strings.TrimSpace(options.telemtImageSource)
	options.mtgImage = strings.TrimSpace(options.mtgImage)
	options.mtgImageSource = strings.TrimSpace(options.mtgImageSource)
	options.extraEnv = envFlag.Values()
	if err := validateTrustBoundaryEnvOverrides("install", options.extraEnv, options.allowTrustBoundaryEnv); err != nil {
		return installCommandOptions{}, err
	}

	if runtime.Provider(options.provider) == runtime.ProviderTelemt {
		if options.mtgImage != "" || options.mtgImageSource != "" {
			return installCommandOptions{}, errorsForProviderFlagConflict("install", options.provider, "--mtg-image", "--mtg-image-source")
		}
		return options, nil
	}

	if options.apiPort > 0 {
		return installCommandOptions{}, fmt.Errorf("install command --api-port is only supported for provider telemt")
	}
	if options.proxyUser != "" {
		return installCommandOptions{}, fmt.Errorf("install command --proxy-user is only supported for provider telemt")
	}
	if options.telemtImage != "" || options.telemtImageSource != "" {
		return installCommandOptions{}, errorsForProviderFlagConflict("install", options.provider, "--telemt-image", "--telemt-image-source")
	}

	return options, nil
}

func errorsForProviderFlagConflict(command string, provider string, flags ...string) error {
	return fmt.Errorf(
		"%s command provider %q does not support %s",
		command,
		provider,
		strings.Join(flags, ", "),
	)
}

func buildInstallEnvPreview(options installCommandOptions) map[string]string {
	preview := copyStringMap(options.extraEnv)
	preview["PROVIDER"] = options.provider
	preview["PORT"] = strconv.Itoa(options.port)
	preview["INSTALL_DIR"] = options.installDir
	putEnvPreviewIfNotEmpty(preview, "TLS_DOMAIN", options.tlsDomain)
	putEnvPreviewIfNotEmpty(preview, "PUBLIC_IP", options.publicIP)
	putEnvPreviewIfNotEmpty(preview, "SECRET", options.secret)
	putEnvPreviewIfNotEmpty(preview, "PROXY_USER", options.proxyUser)
	putEnvPreviewIfNotEmpty(preview, "TELEMT_IMAGE", options.telemtImage)
	putEnvPreviewIfNotEmpty(preview, "TELEMT_IMAGE_SOURCE", options.telemtImageSource)
	putEnvPreviewIfNotEmpty(preview, "MTG_IMAGE", options.mtgImage)
	putEnvPreviewIfNotEmpty(preview, "MTG_IMAGE_SOURCE", options.mtgImageSource)
	if options.apiPort > 0 {
		preview["API_PORT"] = strconv.Itoa(options.apiPort)
	}
	return preview
}

func putEnvPreviewIfNotEmpty(target map[string]string, key string, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	target[key] = trimmed
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func normalizeLogOptional(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "n/a"
	}
	return trimmed
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type envOverrideFlag struct {
	command string
	values  map[string]string
}

func newEnvOverrideFlag(command string) *envOverrideFlag {
	return &envOverrideFlag{
		command: command,
		values:  map[string]string{},
	}
}

func (f *envOverrideFlag) String() string {
	if f == nil || len(f.values) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(f.values))
	for key, value := range f.values {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (f *envOverrideFlag) Set(raw string) error {
	if f == nil {
		return fmt.Errorf("env override collector is not initialized")
	}

	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return fmt.Errorf("%s command --env value must use KEY=VALUE format", f.command)
	}

	key, value, ok := strings.Cut(candidate, "=")
	if !ok {
		return fmt.Errorf("%s command --env value %q must use KEY=VALUE format", f.command, raw)
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%s command --env key must not be empty", f.command)
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return fmt.Errorf("%s command --env key %q must not contain whitespace", f.command, key)
	}

	f.values[key] = strings.TrimSpace(value)
	return nil
}

func (f *envOverrideFlag) Values() map[string]string {
	if f == nil {
		return map[string]string{}
	}
	return copyStringMap(f.values)
}

func validateTrustBoundaryEnvOverrides(command string, envOverrides map[string]string, allowTrustBoundaryEnv bool) error {
	trustBoundaryKeys := make([]string, 0, len(envOverrides))
	for key := range envOverrides {
		normalizedKey := strings.ToUpper(strings.TrimSpace(key))
		if normalizedKey == "" {
			continue
		}
		if _, ok := cliTrustBoundaryEnvKeyAllowlist[normalizedKey]; ok {
			trustBoundaryKeys = append(trustBoundaryKeys, normalizedKey)
			continue
		}
		for _, prefix := range cliTrustBoundaryEnvPrefixAllowlist {
			normalizedPrefix := strings.ToUpper(strings.TrimSpace(prefix))
			if normalizedPrefix == "" {
				continue
			}
			if strings.HasPrefix(normalizedKey, normalizedPrefix) {
				trustBoundaryKeys = append(trustBoundaryKeys, normalizedKey)
				break
			}
		}
	}

	if len(trustBoundaryKeys) == 0 {
		return nil
	}
	sort.Strings(trustBoundaryKeys)
	trustBoundaryKeys = uniqueSortedKeys(trustBoundaryKeys)

	if !allowTrustBoundaryEnv {
		return fmt.Errorf(
			"%s command --env keys cross trust boundary and require --allow-trust-boundary-env: %s",
			command,
			strings.Join(trustBoundaryKeys, ", "),
		)
	}

	privilegedContext := detectCLIPrivilegedContext()
	if privilegedContext != "" {
		return fmt.Errorf(
			"%s command --env keys cross trust boundary and are blocked in privileged context %q: %s",
			command,
			privilegedContext,
			strings.Join(trustBoundaryKeys, ", "),
		)
	}

	return nil
}

func uniqueSortedKeys(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	unique := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		unique = append(unique, trimmed)
	}
	return unique
}

func detectCLIPrivilegedContext() string {
	if value := strings.TrimSpace(os.Getenv("SUDO_UID")); value != "" {
		return "sudo"
	}
	if value := strings.TrimSpace(os.Getenv("SUDO_USER")); value != "" {
		return "sudo"
	}
	if value := strings.TrimSpace(strings.ToLower(os.Getenv("CI"))); value != "" && value != "0" && value != "false" && value != "no" {
		return "ci"
	}

	currentUser, err := user.Current()
	if err == nil {
		if strings.TrimSpace(currentUser.Uid) == "0" {
			return "uid=0"
		}
	}
	return ""
}
