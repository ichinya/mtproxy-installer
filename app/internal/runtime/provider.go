package runtime

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type Provider string

const (
	ProviderTelemt   Provider = "telemt"
	ProviderMTG      Provider = "mtg"
	ProviderOfficial Provider = "official"
)

type ProviderSource string

const (
	ProviderSourceEnv       ProviderSource = "env"
	ProviderSourceHeuristic ProviderSource = "heuristic"
)

type ProviderDescriptor struct {
	Name              Provider
	Source            ProviderSource
	ConfigPath        string
	EnvProvider       string
	TelemtConfigPath  string
	MTGConfigPath     string
	TelemtConfigFound bool
	MTGConfigFound    bool
}

func DetectProvider(installDir string, env *EnvFile, logger *slog.Logger) (ProviderDescriptor, error) {
	logger = fallbackLogger(logger)

	telemtConfigPath := filepath.Join(installDir, "providers", string(ProviderTelemt), "telemt.toml")
	mtgConfigPath := filepath.Join(installDir, "providers", string(ProviderMTG), "mtg.conf")

	telemtExists := pathExists(telemtConfigPath)
	mtgExists := pathExists(mtgConfigPath)

	logger.Debug("provider detection probe", "path", telemtConfigPath, "exists", telemtExists)
	logger.Debug("provider detection probe", "path", mtgConfigPath, "exists", mtgExists)

	envProvider := ""
	if env != nil {
		envProvider = env.ProviderValue()
	}
	logger.Debug("provider lookup from env", "provider", envProvider)

	base := ProviderDescriptor{
		EnvProvider:       envProvider,
		TelemtConfigPath:  telemtConfigPath,
		MTGConfigPath:     mtgConfigPath,
		TelemtConfigFound: telemtExists,
		MTGConfigFound:    mtgExists,
	}

	if envProvider != "" {
		provider, parseErr := parseProvider(envProvider)
		if parseErr != nil {
			err := &RuntimeError{
				Code:     CodeProviderUnsupported,
				Path:     installDir,
				Provider: envProvider,
				Message:  parseErr.Error(),
				Err:      parseErr,
			}
			logger.Error("unsupported provider from env", "provider", envProvider, "error", err.Error())
			return ProviderDescriptor{}, err
		}

		if provider == ProviderOfficial {
			err := &RuntimeError{
				Code:     CodeProviderUnsupported,
				Path:     installDir,
				Provider: envProvider,
				Message:  "provider official is reference-only and unsupported for runtime commands",
			}
			logger.Error("unsupported provider from env", "provider", envProvider, "error", err.Error())
			return ProviderDescriptor{}, err
		}

		expectedConfigPath, configErr := providerConfigPath(installDir, provider)
		if configErr != nil {
			logger.Error("provider config resolution failed", "provider", provider, "error", configErr.Error())
			return ProviderDescriptor{}, configErr
		}
		expectedExists := pathExists(expectedConfigPath)

		logger.Debug("provider env branch", "provider", provider, "expected_config", expectedConfigPath, "expected_exists", expectedExists)

		if !expectedExists {
			otherProvider := oppositeProvider(provider)
			otherConfigPath, _ := providerConfigPath(installDir, otherProvider)
			otherExists := pathExists(otherConfigPath)

			if otherExists {
				mismatchErr := &RuntimeError{
					Code:     CodeProviderMismatch,
					Path:     expectedConfigPath,
					Provider: string(provider),
					Message: fmt.Sprintf(
						"runtime provider mismatch: env declares %s but only %s config exists",
						provider,
						otherProvider,
					),
				}
				logger.Error(
					"provider mismatch detected",
					"env_provider", provider,
					"missing_config", expectedConfigPath,
					"detected_other_provider", otherProvider,
					"other_config", otherConfigPath,
					"error", mismatchErr.Error(),
				)
				return ProviderDescriptor{}, mismatchErr
			}

			missingErr := &RuntimeError{
				Code:     CodeRequiredFileMissing,
				Path:     expectedConfigPath,
				Provider: string(provider),
				Message:  "provider config file is missing",
			}
			logger.Error("provider config missing", "provider", provider, "path", expectedConfigPath, "error", missingErr.Error())
			return ProviderDescriptor{}, missingErr
		}

		descriptor := base
		descriptor.Name = provider
		descriptor.Source = ProviderSourceEnv
		descriptor.ConfigPath = expectedConfigPath

		logger.Info(
			"provider detected",
			"provider", descriptor.Name,
			"source", descriptor.Source,
			"config", descriptor.ConfigPath,
		)
		return descriptor, nil
	}

	logger.Debug("provider lookup fallback to config heuristics")
	switch {
	case telemtExists && !mtgExists:
		descriptor := base
		descriptor.Name = ProviderTelemt
		descriptor.Source = ProviderSourceHeuristic
		descriptor.ConfigPath = telemtConfigPath
		logger.Info("provider detected", "provider", descriptor.Name, "source", descriptor.Source, "config", descriptor.ConfigPath)
		return descriptor, nil
	case mtgExists && !telemtExists:
		descriptor := base
		descriptor.Name = ProviderMTG
		descriptor.Source = ProviderSourceHeuristic
		descriptor.ConfigPath = mtgConfigPath
		logger.Info("provider detected", "provider", descriptor.Name, "source", descriptor.Source, "config", descriptor.ConfigPath)
		return descriptor, nil
	case telemtExists && mtgExists:
		ambiguousErr := &RuntimeError{
			Code:    CodeProviderAmbiguous,
			Path:    installDir,
			Message: "provider detection is ambiguous: both telemt and mtg configs exist while PROVIDER is unset",
		}
		logger.Error(
			"ambiguous provider state",
			"install_dir", installDir,
			"telemt_config", telemtConfigPath,
			"mtg_config", mtgConfigPath,
			"error", ambiguousErr.Error(),
		)
		return ProviderDescriptor{}, ambiguousErr
	default:
		undetectedErr := &RuntimeError{
			Code:    CodeProviderUndetected,
			Path:    installDir,
			Message: "unable to detect installed provider",
		}
		logger.Error(
			"provider detection failed",
			"install_dir", installDir,
			"telemt_config", telemtConfigPath,
			"mtg_config", mtgConfigPath,
			"error", undetectedErr.Error(),
		)
		return ProviderDescriptor{}, undetectedErr
	}
}

func parseProvider(raw string) (Provider, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch Provider(value) {
	case ProviderTelemt, ProviderMTG, ProviderOfficial:
		return Provider(value), nil
	default:
		return "", fmt.Errorf("unsupported provider value %q", raw)
	}
}

func providerConfigPath(installDir string, provider Provider) (string, error) {
	switch provider {
	case ProviderTelemt:
		return filepath.Join(installDir, "providers", string(provider), "telemt.toml"), nil
	case ProviderMTG:
		return filepath.Join(installDir, "providers", string(provider), "mtg.conf"), nil
	default:
		return "", &RuntimeError{
			Code:     CodeProviderUnsupported,
			Path:     installDir,
			Provider: string(provider),
			Message:  fmt.Sprintf("provider %q is not supported", provider),
		}
	}
}

func oppositeProvider(provider Provider) Provider {
	if provider == ProviderTelemt {
		return ProviderMTG
	}
	return ProviderTelemt
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
