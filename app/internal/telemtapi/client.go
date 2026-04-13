package telemtapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultTimeout          = 5 * time.Second
	DefaultMaxResponseBytes = int64(1024 * 1024)
)

var errResponseTooLarge = errors.New("response exceeds configured size limit")

type RequestErrorKind string

const (
	RequestErrorKindTransport        RequestErrorKind = "transport_error"
	RequestErrorKindHTTPStatus       RequestErrorKind = "non_200_status"
	RequestErrorKindResponseTooLarge RequestErrorKind = "response_too_large"
	RequestErrorKindParse            RequestErrorKind = "parse_error"
)

type RequestError struct {
	Kind       RequestErrorKind
	Path       string
	Target     string
	StatusCode int
	Err        error
}

func (e *RequestError) Error() string {
	if e == nil {
		return "telemt api request error"
	}

	switch e.Kind {
	case RequestErrorKindHTTPStatus:
		return fmt.Sprintf("telemt api request failed for %s: unexpected status %d", e.Path, e.StatusCode)
	case RequestErrorKindResponseTooLarge:
		return fmt.Sprintf("telemt api request failed for %s: response exceeds configured limit", e.Path)
	case RequestErrorKindParse:
		return fmt.Sprintf("telemt api request failed for %s: unable to parse payload", e.Path)
	case RequestErrorKindTransport:
		if e.Err != nil {
			return fmt.Sprintf("telemt api request failed for %s: %v", e.Path, e.Err)
		}
		return fmt.Sprintf("telemt api request failed for %s: transport error", e.Path)
	default:
		if e.Err != nil {
			return fmt.Sprintf("telemt api request failed for %s: %v", e.Path, e.Err)
		}
		return fmt.Sprintf("telemt api request failed for %s", e.Path)
	}
}

func (e *RequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type HealthFetch struct {
	Path           string
	Target         string
	Timeout        time.Duration
	HTTPStatus     int
	Payload        HealthEnvelope
	ParseClass     HealthParseClass
	DegradedReason string
}

type UsersFetch struct {
	Path       string
	Target     string
	Timeout    time.Duration
	HTTPStatus int
	Payload    UsersEnvelope
	Selection  LinkSelection
}

type ClientOptions struct {
	BaseURL          string
	Timeout          time.Duration
	HTTPClient       *http.Client
	Logger           *slog.Logger
	MaxResponseBytes int64
}

type Client struct {
	baseURL          string
	timeout          time.Duration
	httpClient       *http.Client
	logger           *slog.Logger
	maxResponseBytes int64
}

func NewClient(options ClientOptions) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("telemt api base URL is required")
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("telemt api base URL is invalid")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("telemt api base URL scheme is unsupported: %s", parsedURL.Scheme)
	}
	if strings.TrimSpace(parsedURL.Host) == "" {
		return nil, errors.New("telemt api base URL host is required")
	}
	if parsedURL.User != nil {
		return nil, errors.New("telemt api base URL must not include userinfo")
	}
	if parsedURL.RawQuery != "" || parsedURL.ForceQuery {
		return nil, errors.New("telemt api base URL must not include query parameters")
	}
	if parsedURL.Fragment != "" {
		return nil, errors.New("telemt api base URL must not include fragment")
	}

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	httpClient = cloneHTTPClientWithSafeRedirects(httpClient)

	return &Client{
		baseURL:          baseURL,
		timeout:          timeout,
		httpClient:       httpClient,
		logger:           fallbackLogger(options.Logger),
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func (c *Client) GetHealth(ctx context.Context) (HealthFetch, error) {
	const path = "/v1/health"

	body, statusCode, target, timeout, err := c.get(ctx, path)
	if err != nil {
		c.logRequestFailure(path, target, timeout, err)
		return HealthFetch{}, err
	}

	var payload HealthEnvelope
	if err := decodeJSON(body, &payload); err != nil {
		requestErr := &RequestError{
			Kind:       RequestErrorKindParse,
			Path:       path,
			Target:     target,
			StatusCode: statusCode,
			Err:        err,
		}
		c.logRequestFailure(path, target, timeout, requestErr)
		return HealthFetch{}, requestErr
	}

	parseClass := payload.ParseClass()
	degradedReason := payload.DegradedReason()
	result := HealthFetch{
		Path:           path,
		Target:         target,
		Timeout:        timeout,
		HTTPStatus:     statusCode,
		Payload:        payload,
		ParseClass:     parseClass,
		DegradedReason: degradedReason,
	}

	c.logger.Debug(
		"telemt api health parse outcome",
		"path", path,
		"target", target,
		"http_status", statusCode,
		"parse_class", parseClass,
		"degraded_reason", degradedReason,
	)

	if parseClass == HealthParseClassComplete {
		c.logger.Info(
			"telemt api health fetched",
			"path", path,
			"target", target,
			"http_status", statusCode,
			"health_ok", payload.IsHealthy(),
			"status", strings.TrimSpace(payload.Data.Status),
			"read_only", *payload.Data.ReadOnly,
		)
	} else {
		c.logger.Warn(
			"telemt api health payload degraded",
			"path", path,
			"target", target,
			"http_status", statusCode,
			"parse_class", parseClass,
			"degraded_reason", degradedReason,
		)
	}

	return result, nil
}

func (c *Client) GetUsers(ctx context.Context) (UsersFetch, error) {
	const path = "/v1/users"

	body, statusCode, target, timeout, err := c.get(ctx, path)
	if err != nil {
		c.logRequestFailure(path, target, timeout, err)
		return UsersFetch{}, err
	}

	var payload UsersEnvelope
	if err := decodeJSON(body, &payload); err != nil {
		requestErr := &RequestError{
			Kind:       RequestErrorKindParse,
			Path:       path,
			Target:     target,
			StatusCode: statusCode,
			Err:        err,
		}
		c.logRequestFailure(path, target, timeout, requestErr)
		return UsersFetch{}, requestErr
	}

	selection := payload.SelectStartupLink()
	result := UsersFetch{
		Path:       path,
		Target:     target,
		Timeout:    timeout,
		HTTPStatus: statusCode,
		Payload:    payload,
		Selection:  selection,
	}

	c.logger.Debug(
		"telemt api users parse outcome",
		"path", path,
		"target", target,
		"http_status", statusCode,
		"parse_class", selection.Class,
		"degraded_reason", selection.DegradedReason,
		"users_count", selection.UsersCount,
		"tls_candidates", selection.CandidateCount,
	)

	if selection.HasUsableLink() {
		c.logger.Info(
			"telemt api startup link fetched",
			"path", path,
			"target", target,
			"http_status", statusCode,
			"parse_class", selection.Class,
			"users_count", selection.UsersCount,
			"tls_candidates", selection.CandidateCount,
			"selected_link", selection.RedactedSelectedLink(),
		)
	} else {
		c.logger.Warn(
			"telemt api users payload degraded",
			"path", path,
			"target", target,
			"http_status", statusCode,
			"parse_class", selection.Class,
			"degraded_reason", selection.DegradedReason,
			"users_count", selection.UsersCount,
			"tls_candidates", selection.CandidateCount,
		)
	}

	return result, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, int, string, time.Duration, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	target := c.baseURL + path
	timeout := c.timeout
	requestCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, target, timeout, &RequestError{
			Kind:   RequestErrorKindTransport,
			Path:   path,
			Target: target,
			Err:    err,
		}
	}

	c.logger.Debug(
		"telemt api request start",
		"path", path,
		"target", target,
		"timeout", timeout.String(),
	)

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, target, timeout, &RequestError{
			Kind:   RequestErrorKindTransport,
			Path:   path,
			Target: target,
			Err:    err,
		}
	}
	defer response.Body.Close()

	body, err := c.readBoundedBody(response.Body)
	if err != nil {
		errorKind := RequestErrorKindTransport
		if errors.Is(err, errResponseTooLarge) {
			errorKind = RequestErrorKindResponseTooLarge
		}
		requestErr := &RequestError{
			Kind:   errorKind,
			Path:   path,
			Target: target,
			Err:    err,
		}
		return nil, response.StatusCode, target, timeout, requestErr
	}

	c.logger.Debug(
		"telemt api request finish",
		"path", path,
		"target", target,
		"timeout", timeout.String(),
		"http_status", response.StatusCode,
		"response_bytes", len(body),
	)

	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, target, timeout, &RequestError{
			Kind:       RequestErrorKindHTTPStatus,
			Path:       path,
			Target:     target,
			StatusCode: response.StatusCode,
		}
	}

	return body, response.StatusCode, target, timeout, nil
}

func (c *Client) readBoundedBody(reader io.Reader) ([]byte, error) {
	limitReader := io.LimitReader(reader, c.maxResponseBytes+1)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxResponseBytes {
		return nil, errResponseTooLarge
	}
	return body, nil
}

func (c *Client) logRequestFailure(path string, target string, timeout time.Duration, err error) {
	var requestErr *RequestError
	if errors.As(err, &requestErr) {
		switch requestErr.Kind {
		case RequestErrorKindHTTPStatus:
			c.logger.Warn(
				"telemt api request returned non-200 status",
				"path", path,
				"target", target,
				"timeout", timeout.String(),
				"http_status", requestErr.StatusCode,
			)
			return
		case RequestErrorKindParse:
			c.logger.Warn(
				"telemt api response parsing failed",
				"path", path,
				"target", target,
				"timeout", timeout.String(),
				"http_status", requestErr.StatusCode,
				"error", requestErr.Error(),
			)
			return
		default:
			c.logger.Error(
				"telemt api request failed",
				"path", path,
				"target", target,
				"timeout", timeout.String(),
				"http_status", requestErr.StatusCode,
				"error", requestErr.Error(),
			)
			return
		}
	}

	c.logger.Error(
		"telemt api request failed",
		"path", path,
		"target", target,
		"timeout", timeout.String(),
		"error", err.Error(),
	)
}

func decodeJSON(raw []byte, destination any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("empty JSON payload")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(destination); err != nil {
		return err
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON payload")
		}
		return err
	}

	return nil
}

func fallbackLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func cloneHTTPClientWithSafeRedirects(source *http.Client) *http.Client {
	if source == nil {
		source = &http.Client{}
	}

	cloned := *source
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &cloned
}
