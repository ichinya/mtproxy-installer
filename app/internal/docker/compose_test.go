package docker

import "testing"

func TestNormalizeComposeSubcommandRejectsUnsupported(t *testing.T) {
	_, err := normalizeComposeSubcommand("exec")
	if err == nil {
		t.Fatalf("expected unsupported subcommand to be rejected")
	}
}

func TestValidateComposeArgsRejectsFreeFormToken(t *testing.T) {
	_, err := validateComposeArgs("logs", []string{"service-name"})
	if err == nil {
		t.Fatalf("expected free-form arg to be rejected")
	}
}

func TestValidateComposeServicesRejectsFlagLikeService(t *testing.T) {
	_, err := validateComposeServices([]string{"--all"})
	if err == nil {
		t.Fatalf("expected flag-like service token to be rejected")
	}
}

func TestValidateComposeArgsAcceptsAllowedFlags(t *testing.T) {
	args, err := validateComposeArgs("up", []string{"--detach", "--pull", "missing"})
	if err != nil {
		t.Fatalf("expected allowed args to pass validation, got %v", err)
	}
	if len(args) != 3 {
		t.Fatalf("expected validated args length 3, got %d", len(args))
	}
}

func TestSanitizeComposeEnvOverridesAllowsDockerProxyVars(t *testing.T) {
	sanitized, err := sanitizeComposeEnvOverrides(map[string]string{
		"DOCKER_HOST": "tcp://docker.internal:2375",
		"http_proxy":  "http://proxy.internal:3128",
		"HOME":        "/root",
	}, composeEnvOverrideAllowlist)
	if err != nil {
		t.Fatalf("expected compose env overrides to be accepted, got %v", err)
	}

	if got := sanitized["DOCKER_HOST"]; got != "tcp://docker.internal:2375" {
		t.Fatalf("unexpected DOCKER_HOST value %q", got)
	}
	if got := sanitized["http_proxy"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected lowercase proxy key to be preserved, got %q", got)
	}
	if got := sanitized["HOME"]; got != "/root" {
		t.Fatalf("unexpected HOME value %q", got)
	}
}

func TestSanitizeComposeEnvOverridesAllowsDockerTLSPrefixOptIn(t *testing.T) {
	sanitized, err := sanitizeComposeEnvOverrides(map[string]string{
		"DOCKER_TLS_CERTDIR": "/certs",
	}, composeEnvOverrideAllowlist)
	if err != nil {
		t.Fatalf("expected DOCKER_TLS* compose opt-in key to be accepted, got %v", err)
	}

	if got := sanitized["DOCKER_TLS_CERTDIR"]; got != "/certs" {
		t.Fatalf("unexpected DOCKER_TLS_CERTDIR value %q", got)
	}
}

func TestSanitizeComposeEnvOverridesRejectsUnsafeValue(t *testing.T) {
	_, err := sanitizeComposeEnvOverrides(map[string]string{
		"DOCKER_CONTEXT": "prod\nother",
	}, composeEnvOverrideAllowlist)
	if err == nil {
		t.Fatalf("expected compose env override with newline to be rejected")
	}
}

func TestSanitizeComposeEnvOverridesRejectsRestrictedKeys(t *testing.T) {
	_, err := sanitizeComposeEnvOverrides(map[string]string{
		"PATH": "/custom/bin:/usr/bin",
	}, composeEnvOverrideAllowlist)
	if err == nil {
		t.Fatalf("expected PATH override to be rejected for compose adapter")
	}

	_, err = sanitizeComposeEnvOverrides(map[string]string{
		"DOCKER_CONFIG": "/tmp/user-config",
	}, composeEnvOverrideAllowlist)
	if err == nil {
		t.Fatalf("expected DOCKER_CONFIG override to be rejected for compose adapter")
	}
}
