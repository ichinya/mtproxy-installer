package telemt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"mtproxy-installer/app/internal/runtime"
	"mtproxy-installer/app/internal/telemtapi"
)

const (
	DefaultControlAPIPort = 9091
	DefaultControlTimeout = 5 * time.Second
)

type APIOptions struct {
	Runtime    *runtime.RuntimeInstallation
	Timeout    time.Duration
	HTTPClient *http.Client
	Logger     *slog.Logger
}

type API struct {
	runtimeState    *runtime.RuntimeInstallation
	logger          *slog.Logger
	client          *telemtapi.Client
	controlEndpoint string
	timeout         time.Duration
	apiPort         int
	defaultPortUsed bool
}

func NewAPI(options APIOptions) (*API, error) {
	if options.Runtime == nil {
		return nil, errors.New("runtime installation is required for telemt api bridge")
	}
	if options.Runtime.Provider.Name != runtime.ProviderTelemt {
		return nil, fmt.Errorf("telemt api bridge requires provider telemt, got %q", options.Runtime.Provider.Name)
	}

	logger := fallbackLogger(options.Logger)
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultControlTimeout
	}

	port, defaultPortUsed, err := resolveControlAPIPort(options.Runtime)
	if err != nil {
		return nil, err
	}
	controlEndpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	client, err := telemtapi.NewClient(telemtapi.ClientOptions{
		BaseURL:    controlEndpoint,
		Timeout:    timeout,
		HTTPClient: options.HTTPClient,
		Logger:     logger,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to initialize telemt api client: %w", err)
	}

	logger.Debug(
		"telemt provider api bridge initialized",
		"provider", options.Runtime.Provider.Name,
		"install_dir", options.Runtime.Paths.InstallDir,
		"control_endpoint", controlEndpoint,
		"api_port", port,
		"default_api_port_used", defaultPortUsed,
		"timeout", timeout.String(),
	)

	return &API{
		runtimeState:    options.Runtime,
		logger:          logger,
		client:          client,
		controlEndpoint: controlEndpoint,
		timeout:         timeout,
		apiPort:         port,
		defaultPortUsed: defaultPortUsed,
	}, nil
}

func (a *API) ControlEndpoint() string {
	return a.controlEndpoint
}

func (a *API) ReadHealth(ctx context.Context) (telemtapi.HealthFetch, error) {
	a.logger.Debug(
		"telemt provider health read start",
		"provider", a.runtimeState.Provider.Name,
		"control_endpoint", a.controlEndpoint,
		"api_port", a.apiPort,
		"default_api_port_used", a.defaultPortUsed,
		"timeout", a.timeout.String(),
	)

	result, err := a.client.GetHealth(ctx)
	if err != nil {
		a.logger.Error(
			"telemt provider health read failed",
			"provider", a.runtimeState.Provider.Name,
			"control_endpoint", a.controlEndpoint,
			"timeout", a.timeout.String(),
			"error", err.Error(),
		)
		return telemtapi.HealthFetch{}, err
	}

	if result.ParseClass == telemtapi.HealthParseClassComplete {
		a.logger.Info(
			"telemt provider health read resolved",
			"provider", a.runtimeState.Provider.Name,
			"control_endpoint", a.controlEndpoint,
			"http_status", result.HTTPStatus,
			"parse_class", result.ParseClass,
			"health_ok", result.Payload.IsHealthy(),
		)
	} else {
		a.logger.Warn(
			"telemt provider health read degraded",
			"provider", a.runtimeState.Provider.Name,
			"control_endpoint", a.controlEndpoint,
			"http_status", result.HTTPStatus,
			"parse_class", result.ParseClass,
			"degraded_reason", result.DegradedReason,
		)
	}

	return result, nil
}

func (a *API) ResolveStartupLink(ctx context.Context) (telemtapi.UsersFetch, error) {
	a.logger.Debug(
		"telemt provider startup link read start",
		"provider", a.runtimeState.Provider.Name,
		"control_endpoint", a.controlEndpoint,
		"api_port", a.apiPort,
		"default_api_port_used", a.defaultPortUsed,
		"timeout", a.timeout.String(),
	)

	result, err := a.client.GetUsers(ctx)
	if err != nil {
		a.logger.Error(
			"telemt provider startup link read failed",
			"provider", a.runtimeState.Provider.Name,
			"control_endpoint", a.controlEndpoint,
			"timeout", a.timeout.String(),
			"error", err.Error(),
		)
		return telemtapi.UsersFetch{}, err
	}

	if result.Selection.HasUsableLink() {
		a.logger.Info(
			"telemt provider startup link resolved",
			"provider", a.runtimeState.Provider.Name,
			"control_endpoint", a.controlEndpoint,
			"http_status", result.HTTPStatus,
			"parse_class", result.Selection.Class,
			"users_count", result.Selection.UsersCount,
			"tls_candidates", result.Selection.CandidateCount,
			"selected_link", result.Selection.RedactedSelectedLink(),
		)
	} else {
		a.logger.Warn(
			"telemt provider startup link degraded",
			"provider", a.runtimeState.Provider.Name,
			"control_endpoint", a.controlEndpoint,
			"http_status", result.HTTPStatus,
			"parse_class", result.Selection.Class,
			"degraded_reason", result.Selection.DegradedReason,
			"users_count", result.Selection.UsersCount,
			"tls_candidates", result.Selection.CandidateCount,
		)
	}

	return result, nil
}

func resolveControlAPIPort(runtimeState *runtime.RuntimeInstallation) (int, bool, error) {
	if runtimeState == nil || runtimeState.Env == nil {
		return DefaultControlAPIPort, true, nil
	}

	port, provided, err := runtimeState.Env.APIPort()
	if err != nil {
		return 0, false, fmt.Errorf("unable to resolve API_PORT from runtime env: %w", err)
	}
	if !provided {
		return DefaultControlAPIPort, true, nil
	}
	if port < 1 || port > 65535 {
		return 0, false, fmt.Errorf("runtime API_PORT is out of range: %d", port)
	}
	return port, false, nil
}

func fallbackLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
