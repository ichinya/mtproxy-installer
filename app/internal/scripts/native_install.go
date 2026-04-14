package scripts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	execadapter "mtproxy-installer/app/internal/exec"
	telemtprovider "mtproxy-installer/app/internal/provider/telemt"
	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

type installNativeConfig struct {
	provider          runtime.Provider
	installDir        string
	providerDir       string
	dataDir           string
	port              int
	publicIP          string
	secret            string
	apiPort           int
	tlsDomain         string
	proxyUser         string
	rustLog           string
	mtgDebug          string
	telemtImage       string
	telemtImageSource string
	mtgImage          string
	mtgImageSource    string
}

func (m *Manager) installGoNative(
	ctx context.Context,
	options InstallOptions,
	provider string,
	installDir string,
	envOverrides map[string]string,
) (execadapter.Result, error) {
	transcript := newLifecycleTranscript()
	resultArgs := []string{"install", provider, strconv.Itoa(options.Port)}

	dockerPath, err := m.runLifecyclePreflight(ctx, "install", installDir, envOverrides)
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	cfg, err := m.resolveInstallNativeConfig(ctx, dockerPath, options, provider, installDir, envOverrides)
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	if err := m.prepareInstallLayout(cfg); err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	if err := m.writeInstallContractFiles(cfg); err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	switch cfg.provider {
	case runtime.ProviderTelemt:
		transcript.stdoutLine(fmt.Sprintf("Pulling Telemt image source: %s", cfg.telemtImageSource))
		cfg.telemtImage, err = m.resolveImageReference(ctx, dockerPath, cfg.telemtImageSource, envOverrides)
		if err == nil {
			err = upsertEnvFileValue(filepath.Join(cfg.installDir, ".env"), "TELEMT_IMAGE_SOURCE", cfg.telemtImageSource)
		}
		if err == nil {
			err = upsertEnvFileValue(filepath.Join(cfg.installDir, ".env"), "TELEMT_IMAGE", cfg.telemtImage)
		}
	case runtime.ProviderMTG:
		transcript.stdoutLine(fmt.Sprintf("Pulling mtg image source: %s", cfg.mtgImageSource))
		cfg.mtgImage, err = m.resolveImageReference(ctx, dockerPath, cfg.mtgImageSource, envOverrides)
		if err == nil {
			err = upsertEnvFileValue(filepath.Join(cfg.installDir, ".env"), "MTG_IMAGE_SOURCE", cfg.mtgImageSource)
		}
		if err == nil {
			err = upsertEnvFileValue(filepath.Join(cfg.installDir, ".env"), "MTG_IMAGE", cfg.mtgImage)
		}
		if err == nil {
			transcript.stdoutLine("[FIX] Validating generated mtg config with mtg access.")
			_, err = m.validateMTGConfig(ctx, dockerPath, cfg, envOverrides)
		}
	}
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	runtimeState, err := runtime.Load(runtime.LoadOptions{
		InstallDir: cfg.installDir,
		Logger:     m.logger,
	})
	if err != nil {
		writeLifecycleFailure(transcript, err)
		result := transcript.result(resultArgs, 1)
		return result, lifecycleCommandError(result, err)
	}

	_, _ = m.runComposeCommand(ctx, dockerPath, runtimeState, envOverrides, "down")
	upResult, upErr := m.runComposeCommand(ctx, dockerPath, runtimeState, envOverrides, "up", "-d", "--force-recreate")
	transcript.appendResult(upResult)
	if upErr != nil {
		result := transcript.result(resultArgs, upResult.ExitCode)
		return result, lifecycleCommandError(result, upErr)
	}

	proxyLink := ""
	if cfg.provider == runtime.ProviderTelemt {
		usersFetch, linkErr := m.waitForTelemtUsers(ctx, runtimeState)
		if linkErr == nil && usersFetch.Selection.HasUsableLink() {
			proxyLink = usersFetch.Selection.SelectedLink
		}
	}
	if cfg.provider == runtime.ProviderMTG {
		proxyLink = buildMTGProxyLink(cfg.publicIP, cfg.port, cfg.secret)
	}

	m.writeInstallSummary(transcript, cfg, runtimeState, proxyLink)
	return transcript.result(resultArgs, 0), nil
}

func (m *Manager) resolveInstallNativeConfig(
	ctx context.Context,
	dockerPath string,
	options InstallOptions,
	provider string,
	installDir string,
	envOverrides map[string]string,
) (installNativeConfig, error) {
	cfg := installNativeConfig{
		provider:    runtime.Provider(provider),
		installDir:  installDir,
		providerDir: filepath.Join(installDir, "providers", provider),
		dataDir:     filepath.Join(installDir, "providers", provider, "data"),
		port:        options.Port,
	}

	if cfg.provider == runtime.ProviderTelemt {
		cfg.apiPort = defaultTelemtAPIPort
		if options.APIPort > 0 {
			cfg.apiPort = options.APIPort
		}
		cfg.proxyUser = firstNonEmpty(options.ProxyUser, defaultTelemtProxyUser)
		cfg.rustLog = firstNonEmpty(envOverrides["RUST_LOG"], "info")
		cfg.telemtImageSource = firstNonEmpty(options.TelemtImageSource, options.TelemtImage, defaultTelemtImageSource)
		cfg.telemtImage = cfg.telemtImageSource
	}
	if cfg.provider == runtime.ProviderMTG {
		cfg.mtgDebug = firstNonEmpty(envOverrides["MTG_DEBUG"], "info")
		cfg.mtgImageSource = firstNonEmpty(options.MTGImageSource, options.MTGImage, defaultMTGImageSource)
		cfg.mtgImage = cfg.mtgImageSource
	}

	cfg.tlsDomain = firstNonEmpty(options.TLSDomain, defaultTelemtTLSDomain)
	cfg.publicIP = strings.TrimSpace(options.PublicIP)
	if cfg.publicIP == "" {
		publicIP, err := resolvePublicIP(ctx)
		if err != nil {
			return installNativeConfig{}, newLifecyclePreflightError(
				"install",
				"public_ip",
				"automatic public IP detection failed",
				"pass --public-ip explicitly if the host has restricted outbound access",
				err,
			)
		}
		cfg.publicIP = publicIP
	}

	cfg.secret = strings.TrimSpace(options.Secret)
	if cfg.secret == "" {
		if cfg.provider == runtime.ProviderTelemt {
			secret, err := generateTelemtSecret()
			if err != nil {
				return installNativeConfig{}, err
			}
			cfg.secret = secret
		} else {
			secret, err := m.generateMTGSecret(ctx, dockerPath, cfg.mtgImageSource, cfg.tlsDomain, envOverrides)
			if err != nil {
				return installNativeConfig{}, err
			}
			cfg.secret = secret
		}
	}

	return cfg, nil
}

func (m *Manager) prepareInstallLayout(cfg installNativeConfig) error {
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return err
	}
	if cfg.provider == runtime.ProviderTelemt {
		if err := os.MkdirAll(filepath.Join(cfg.dataDir, "cache"), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(cfg.dataDir, "tlsfront"), 0o755); err != nil {
			return err
		}
	}
	if err := chownRecursive(cfg.dataDir, telemtDataOwnerUID, telemtDataOwnerGID); err != nil {
		return err
	}

	backupTargets := []string{
		filepath.Join(cfg.installDir, ".env"),
		filepath.Join(cfg.installDir, "docker-compose.yml"),
	}
	switch cfg.provider {
	case runtime.ProviderTelemt:
		backupTargets = append(backupTargets, filepath.Join(cfg.providerDir, "telemt.toml"))
	case runtime.ProviderMTG:
		backupTargets = append(backupTargets, filepath.Join(cfg.providerDir, "mtg.conf"))
	}

	for _, target := range backupTargets {
		if err := backupFileIfExists(target); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) writeInstallContractFiles(cfg installNativeConfig) error {
	envPath := filepath.Join(cfg.installDir, ".env")
	composePath := filepath.Join(cfg.installDir, "docker-compose.yml")

	switch cfg.provider {
	case runtime.ProviderTelemt:
		if err := writeFileAtomically(envPath, renderTelemtEnv(cfg), 0o644); err != nil {
			return err
		}
		if err := writeFileAtomically(filepath.Join(cfg.providerDir, "telemt.toml"), renderTelemtConfig(cfg), 0o644); err != nil {
			return err
		}
		return writeFileAtomically(composePath, renderTelemtCompose(), 0o644)
	case runtime.ProviderMTG:
		if err := writeFileAtomically(envPath, renderMTGEnv(cfg), 0o644); err != nil {
			return err
		}
		if err := writeFileAtomically(filepath.Join(cfg.providerDir, "mtg.conf"), renderMTGConfig(cfg), 0o644); err != nil {
			return err
		}
		return writeFileAtomically(composePath, renderMTGCompose(), 0o644)
	default:
		return fmt.Errorf("unsupported provider for native install: %s", cfg.provider)
	}
}

func (m *Manager) writeInstallSummary(
	transcript *lifecycleTranscript,
	cfg installNativeConfig,
	runtimeState *runtime.RuntimeInstallation,
	proxyLink string,
) {
	transcript.stdoutLine("")
	transcript.stdoutLine("==============================")
	transcript.stdoutLine(fmt.Sprintf("%s installed", cfg.provider))
	transcript.stdoutLine("==============================")
	transcript.stdoutLine("")
	transcript.stdoutLine(fmt.Sprintf("Install dir: %s", cfg.installDir))
	transcript.stdoutLine(fmt.Sprintf("Provider: %s", cfg.provider))
	transcript.stdoutLine(fmt.Sprintf("Public endpoint: %s:%d", cfg.publicIP, cfg.port))
	transcript.stdoutLine(fmt.Sprintf("Secret: %s", cfg.secret))
	transcript.stdoutLine("")
	if strings.TrimSpace(proxyLink) != "" {
		transcript.stdoutLine("Proxy link:")
		transcript.stdoutLine(proxyLink)
		transcript.stdoutLine("")
	}

	switch cfg.provider {
	case runtime.ProviderTelemt:
		transcript.stdoutLine(fmt.Sprintf("API: http://127.0.0.1:%d/v1/health", cfg.apiPort))
		transcript.stdoutLine(fmt.Sprintf("Config: %s", runtimeState.Provider.ConfigPath))
		transcript.stdoutLine("")
		transcript.stdoutLine("[FIX] Telegram voice calls are not guaranteed over MTProto proxy.")
	case runtime.ProviderMTG:
		transcript.stdoutLine(fmt.Sprintf("Config: %s", runtimeState.Provider.ConfigPath))
		transcript.stdoutLine("")
		transcript.stdoutLine("[FIX] mtg v2 does not support ad_tag.")
		transcript.stdoutLine("[FIX] mtg has no HTTP API for automatic link extraction.")
		transcript.stdoutLine("[FIX] Telegram voice calls are not guaranteed over MTProto proxy.")
	}

	transcript.stdoutLine("")
	transcript.stdoutLine(fmt.Sprintf("Logs: docker compose -f %s logs -f %s", runtimeState.Paths.ComposeFile, cfg.provider))
}

func resolvePublicIP(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("public IP lookup failed with status %d", response.StatusCode)
	}
	publicIP := strings.TrimSpace(string(body))
	if publicIP == "" {
		return "", fmt.Errorf("public IP lookup returned empty response")
	}
	return publicIP, nil
}

func generateTelemtSecret() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (m *Manager) generateMTGSecret(
	ctx context.Context,
	dockerPath string,
	imageRef string,
	tlsDomain string,
	envOverrides map[string]string,
) (string, error) {
	result, err := m.runDockerCommand(ctx, dockerPath, m.repoRoot, envOverrides, "run", "--rm", imageRef, "generate-secret", tlsDomain)
	if err != nil {
		return "", err
	}
	secret := firstNonEmptyLine(result.Stdout)
	if secret == "" {
		return "", fmt.Errorf("mtg secret generation returned empty output")
	}
	return secret, nil
}

func (m *Manager) resolveImageReference(
	ctx context.Context,
	dockerPath string,
	sourceRef string,
	envOverrides map[string]string,
) (string, error) {
	_, err := m.runDockerCommand(ctx, dockerPath, m.repoRoot, envOverrides, "pull", sourceRef)
	if err != nil {
		return "", err
	}
	result, err := m.runDockerCommand(
		ctx,
		dockerPath,
		m.repoRoot,
		envOverrides,
		"image",
		"inspect",
		"--format",
		"{{range .RepoDigests}}{{println .}}{{end}}",
		sourceRef,
	)
	if err != nil {
		return "", err
	}
	pinned := firstNonEmptyLine(result.Stdout)
	if pinned == "" {
		return sourceRef, nil
	}
	return pinned, nil
}

func (m *Manager) validateMTGConfig(
	ctx context.Context,
	dockerPath string,
	cfg installNativeConfig,
	envOverrides map[string]string,
) (execadapter.Result, error) {
	configPath := filepath.Join(cfg.providerDir, "mtg.conf")
	return m.runDockerCommand(
		ctx,
		dockerPath,
		cfg.installDir,
		envOverrides,
		"run",
		"--rm",
		"-v",
		fmt.Sprintf("%s:/config.toml:ro", configPath),
		cfg.mtgImage,
		"access",
		"/config.toml",
	)
}

func (m *Manager) waitForTelemtUsers(
	ctx context.Context,
	runtimeState *runtime.RuntimeInstallation,
) (telemtapi.UsersFetch, error) {
	api, err := telemtprovider.NewAPI(telemtprovider.APIOptions{
		Runtime: runtimeState,
		Logger:  m.logger,
	})
	if err != nil {
		return telemtapi.UsersFetch{}, err
	}

	var lastErr error
	for attempt := 0; attempt < defaultInstallPollAttempts; attempt++ {
		_, healthErr := api.ReadHealth(ctx)
		usersFetch, usersErr := api.ResolveStartupLink(ctx)
		if healthErr == nil && usersErr == nil {
			return usersFetch, nil
		}
		if usersErr != nil {
			lastErr = usersErr
		} else if healthErr != nil {
			lastErr = healthErr
		}
		if ctx != nil && ctx.Err() != nil {
			return telemtapi.UsersFetch{}, ctx.Err()
		}
		time.Sleep(defaultInstallPollDelay)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("telemt API did not become ready")
	}
	return telemtapi.UsersFetch{}, lastErr
}

func chownRecursive(root string, uid int, gid int) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func renderTelemtEnv(cfg installNativeConfig) string {
	return fmt.Sprintf(
		"PROVIDER=telemt\nPORT=%d\nAPI_PORT=%d\nPUBLIC_IP=%s\nTELEMT_IMAGE_SOURCE=%s\nTELEMT_IMAGE=%s\nRUST_LOG=%s\nTLS_DOMAIN=%s\nPROXY_USER=%s\nSECRET=%s\n",
		cfg.port,
		cfg.apiPort,
		cfg.publicIP,
		cfg.telemtImageSource,
		cfg.telemtImage,
		cfg.rustLog,
		cfg.tlsDomain,
		cfg.proxyUser,
		cfg.secret,
	)
}

func renderTelemtConfig(cfg installNativeConfig) string {
	return fmt.Sprintf(`[general]
use_middle_proxy = true
proxy_secret_path = "/var/lib/telemt/proxy-secret"
middle_proxy_nat_ip = "%s"
middle_proxy_nat_probe = true
log_level = "normal"

[general.modes]
classic = false
secure = false
tls = true

[general.links]
show = "*"
public_host = "%s"
public_port = %d

[server]
port = 443
listen_addr_ipv4 = "0.0.0.0"
listen_addr_ipv6 = "::"
proxy_protocol = false
metrics_whitelist = ["127.0.0.1/32", "::1/128"]

[server.api]
enabled = true
listen = "0.0.0.0:%d"
whitelist = []
read_only = true

[[server.listeners]]
ip = "0.0.0.0"
announce = "%s"

[censorship]
tls_domain = "%s"
mask = true
mask_port = 443
fake_cert_len = 2048
tls_emulation = false
tls_front_dir = "/var/lib/telemt/tlsfront"

[access.users]
"%s" = "%s"
`,
		cfg.publicIP,
		cfg.publicIP,
		cfg.port,
		cfg.apiPort,
		cfg.publicIP,
		cfg.tlsDomain,
		cfg.proxyUser,
		cfg.secret,
	)
}

func renderTelemtCompose() string {
	return `services:
  telemt:
    image: ${TELEMT_IMAGE}
    container_name: telemt
    restart: unless-stopped
    environment:
      RUST_LOG: ${RUST_LOG}
    volumes:
      - ./providers/telemt/telemt.toml:/etc/telemt.toml:ro
      - ./providers/telemt/data:/var/lib/telemt
    ports:
      - "${PORT}:443/tcp"
      - "127.0.0.1:${API_PORT}:9091/tcp"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,noexec,size=16m
    ulimits:
      nofile:
        soft: 65536
        hard: 65536
`
}

func renderMTGEnv(cfg installNativeConfig) string {
	return fmt.Sprintf(
		"PROVIDER=mtg\nPORT=%d\nPUBLIC_IP=%s\nMTG_IMAGE_SOURCE=%s\nMTG_IMAGE=%s\nMTG_DEBUG=%s\nTLS_DOMAIN=%s\nSECRET=%s\n",
		cfg.port,
		cfg.publicIP,
		cfg.mtgImageSource,
		cfg.mtgImage,
		cfg.mtgDebug,
		cfg.tlsDomain,
		cfg.secret,
	)
}

func renderMTGConfig(cfg installNativeConfig) string {
	debugFlag := "false"
	switch strings.ToLower(strings.TrimSpace(cfg.mtgDebug)) {
	case "debug", "true", "1":
		debugFlag = "true"
	}

	return fmt.Sprintf("secret = %q\nbind-to = \"0.0.0.0:3128\"\ndebug = %s\n", cfg.secret, debugFlag)
}

func renderMTGCompose() string {
	return `services:
  mtg:
    image: ${MTG_IMAGE:-nineseconds/mtg:2}
    container_name: mtg
    restart: unless-stopped
    volumes:
      - ./providers/mtg/mtg.conf:/config.toml:ro
      - ./providers/mtg/data:/var/lib/mtg
    ports:
      - "${PORT}:3128/tcp"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,noexec,size=16m
    ulimits:
      nofile:
        soft: 65536
        hard: 65536
`
}

func buildMTGProxyLink(publicIP string, port int, secret string) string {
	return fmt.Sprintf(
		"tg://proxy?server=%s&port=%d&secret=%s",
		publicIP,
		port,
		url.QueryEscape(secret),
	)
}
