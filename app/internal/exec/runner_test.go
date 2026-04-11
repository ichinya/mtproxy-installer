package exec

import (
	"context"
	"strings"
	"testing"
)

func TestBuildCommandEnvPreservesPathAndRequiresExplicitDockerOptIn(t *testing.T) {
	base := []string{
		"PATH=/custom/bin:/usr/bin",
		"DOCKER_HOST=tcp://docker.internal:2375",
		"HTTP_PROXY=http://proxy.internal:3128",
	}
	allowlist := []string{"PATH", "DOCKER_HOST", "HTTP_PROXY"}

	env, blocked := buildCommandEnv(base, allowlist, nil, map[string]string{
		"NO_PROXY":    "localhost,127.0.0.1",
		"DOCKER_HOST": "tcp://docker.internal:2375",
	}, false, "")
	envMap := envListToMap(env)

	if got := envMap["PATH"]; got != "/custom/bin:/usr/bin" {
		t.Fatalf("expected PATH to be preserved, got %q", got)
	}
	if got := envMap["DOCKER_HOST"]; got != "tcp://docker.internal:2375" {
		t.Fatalf("expected explicit DOCKER_HOST override to be preserved, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if _, ok := envMap["NO_PROXY"]; ok {
		t.Fatalf("did not expect NO_PROXY without allowlist entry")
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvDoesNotImplicitlyInheritParentPath(t *testing.T) {
	t.Setenv("PATH", "/tmp/inherited/bin")

	env, blocked := buildCommandEnv(nil, nil, nil, nil, false, "")
	envMap := envListToMap(env)

	if got := envMap["PATH"]; got != normalizedExecutablePath {
		t.Fatalf("expected PATH fallback to normalized executable path, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvUsesNormalizedPathWhenPathNotAllowlisted(t *testing.T) {
	t.Setenv("PATH", "/tmp/inherited/bin")

	env, blocked := buildCommandEnv(
		[]string{"HTTP_PROXY=http://proxy.internal:3128"},
		[]string{"HTTP_PROXY"},
		nil,
		nil,
		false,
		"",
	)
	envMap := envListToMap(env)

	if got := envMap["PATH"]; got != normalizedExecutablePath {
		t.Fatalf("expected PATH to use normalized executable path when PATH is not allowlisted, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestFilterInheritedSensitiveRuntimeEnvRemovesPrivilegedDefaults(t *testing.T) {
	filtered, stripped := filterInheritedSensitiveRuntimeEnv([]string{
		"PATH=/custom/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.internal:3128",
		"HOME=/root",
		"XDG_RUNTIME_DIR=/run/user/0",
		"DOCKER_HOST=tcp://docker.internal:2375",
		"DOCKER_CONTEXT=prod",
		"DOCKER_TLS=1",
		"DOCKER_CERT_PATH=/etc/docker/certs",
		"DOCKER_TLS_CERTDIR=/certs",
		"DOCKER_TLS_VERIFY=1",
		"COMPOSE_PROJECT_NAME=mtproxy",
		"COMPOSE_PROFILES=prod",
	})
	envMap := envListToMap(filtered)

	if got := envMap["PATH"]; got != "/custom/bin:/usr/bin" {
		t.Fatalf("expected PATH to be preserved, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if _, ok := envMap["HOME"]; ok {
		t.Fatalf("expected HOME to be stripped from inherited parent env")
	}
	if _, ok := envMap["XDG_RUNTIME_DIR"]; ok {
		t.Fatalf("expected XDG_RUNTIME_DIR to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_HOST"]; ok {
		t.Fatalf("expected DOCKER_HOST to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_CONTEXT"]; ok {
		t.Fatalf("expected DOCKER_CONTEXT to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_TLS"]; ok {
		t.Fatalf("expected DOCKER_TLS to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_CERT_PATH"]; ok {
		t.Fatalf("expected DOCKER_CERT_PATH to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_TLS_CERTDIR"]; ok {
		t.Fatalf("expected DOCKER_TLS_CERTDIR to be stripped from inherited parent env")
	}
	if _, ok := envMap["DOCKER_TLS_VERIFY"]; ok {
		t.Fatalf("expected DOCKER_TLS_VERIFY to be stripped from inherited parent env")
	}
	if _, ok := envMap["COMPOSE_PROJECT_NAME"]; ok {
		t.Fatalf("expected COMPOSE_PROJECT_NAME to be stripped from inherited parent env")
	}
	if _, ok := envMap["COMPOSE_PROFILES"]; ok {
		t.Fatalf("expected COMPOSE_PROFILES to be stripped from inherited parent env")
	}

	expectedStripped := []string{
		"COMPOSE_PROFILES",
		"COMPOSE_PROJECT_NAME",
		"DOCKER_CERT_PATH",
		"DOCKER_CONTEXT",
		"DOCKER_HOST",
		"DOCKER_TLS",
		"DOCKER_TLS_CERTDIR",
		"DOCKER_TLS_VERIFY",
		"HOME",
		"XDG_RUNTIME_DIR",
	}
	if len(stripped) != len(expectedStripped) {
		t.Fatalf("expected stripped key count %d, got %d (%v)", len(expectedStripped), len(stripped), stripped)
	}
	for idx, key := range expectedStripped {
		if stripped[idx] != key {
			t.Fatalf("unexpected stripped key order/content at %d: got %q want %q", idx, stripped[idx], key)
		}
	}
}

func TestBuildCommandEnvBlocksDangerousShellEnv(t *testing.T) {
	env, blocked := buildCommandEnv(
		[]string{"PATH=/usr/bin"},
		[]string{"PATH", "BASH_ENV"},
		nil,
		map[string]string{
			"BASH_ENV": "/tmp/evil.sh",
		},
		false,
		"",
	)

	envMap := envListToMap(env)
	if _, ok := envMap["BASH_ENV"]; ok {
		t.Fatalf("expected BASH_ENV to be blocked")
	}
	if len(blocked) == 0 {
		t.Fatalf("expected blocked env keys to include BASH_ENV")
	}
}

func TestRunRejectsUnsupportedEnvOverrides(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:          "echo",
		Args:             []string{"ok"},
		WorkingDir:       ".",
		InheritParentEnv: true,
		AllowedEnvKeys:   []string{"PATH"},
		EnvOverrides: map[string]string{
			"INSTALL_DIR": "/opt/mtproxy-installer",
		},
	})
	if err == nil {
		t.Fatalf("expected unsupported env override to fail")
	}
	if !strings.Contains(err.Error(), "env override keys are not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsEnvOverridesWithoutAllowlist(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"INSTALL_DIR": "/opt/mtproxy-installer",
		},
	})
	if err == nil {
		t.Fatalf("expected env overrides without allowlist to fail")
	}
	if !strings.Contains(err.Error(), "AllowedEnvKeys or AllowedEnvPrefixes is required when EnvOverrides are provided") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsSensitiveInheritedAllowlistSelection(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:          "echo",
		Args:             []string{"ok"},
		WorkingDir:       ".",
		InheritParentEnv: true,
		AllowedEnvKeys:   []string{"PATH", "DOCKER_HOST"},
	})
	if err == nil {
		t.Fatalf("expected sensitive inherited allowlist selection to fail")
	}
	if !strings.Contains(err.Error(), "inherited sensitive env selection is not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsSensitiveInheritedPrefixSelection(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:            "echo",
		Args:               []string{"ok"},
		WorkingDir:         ".",
		InheritParentEnv:   true,
		AllowedEnvKeys:     []string{"PATH"},
		AllowedEnvPrefixes: []string{"DOCKER_TLS_"},
	})
	if err == nil {
		t.Fatalf("expected sensitive inherited prefix selection to fail")
	}
	if !strings.Contains(err.Error(), "inherited sensitive env selection is not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsSensitiveOverrideWithoutExplicitKeyAllowlist(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"DOCKER_TLS_CERTDIR": "/certs",
		},
		AllowedEnvPrefixes: []string{"DOCKER_TLS_"},
	})
	if err == nil {
		t.Fatalf("expected sensitive override without explicit key allowlist to fail")
	}
	if !strings.Contains(err.Error(), "sensitive env override keys require explicit AllowedEnvKeys opt-in") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAllowsSensitiveOverrideWithExplicitKeyAllowlist(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"DOCKER_TLS_CERTDIR": "/certs",
		},
		AllowedEnvKeys: []string{"DOCKER_TLS_CERTDIR"},
	})
	if err != nil {
		t.Fatalf("expected explicit sensitive key allowlist to pass, got %v", err)
	}
}

func TestRunRejectsUnsafeEnvOverrideValue(t *testing.T) {
	runner := NewRunner(nil)
	_, err := runner.Run(context.Background(), Request{
		Command:    "echo",
		Args:       []string{"ok"},
		WorkingDir: ".",
		EnvOverrides: map[string]string{
			"INSTALL_DIR": "/opt/mtproxy-installer\nBAD=1",
		},
		AllowedEnvKeys: []string{"INSTALL_DIR"},
	})
	if err == nil {
		t.Fatalf("expected unsafe env override value to fail")
	}
	if !strings.Contains(err.Error(), "contains unsafe characters") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildCommandEnvUsesSafePathForPrivilegedRun(t *testing.T) {
	env, blocked := buildCommandEnv(
		[]string{"PATH=/custom/bin", "HTTP_PROXY=http://proxy.internal:3128"},
		[]string{"PATH", "HTTP_PROXY"},
		nil,
		map[string]string{
			"PATH":       "/tmp/evil",
			"HTTP_PROXY": "http://proxy.internal:3128",
		},
		true,
		"/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)

	envMap := envListToMap(env)
	if got := envMap["PATH"]; got != "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Fatalf("expected safe PATH to override inherited/override values, got %q", got)
	}
	if got := envMap["HTTP_PROXY"]; got != "http://proxy.internal:3128" {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvClearsPrivilegedEnvByDefault(t *testing.T) {
	allowlist := []string{
		"HOME",
		"XDG_RUNTIME_DIR",
		"DOCKER_HOST",
		"DOCKER_CONTEXT",
		"DOCKER_TLS_VERIFY",
		"COMPOSE_PROJECT_NAME",
		"COMPOSE_PROFILES",
	}

	env, blocked := buildCommandEnv(
		nil,
		allowlist,
		[]string{"DOCKER_TLS_"},
		map[string]string{
			"DOCKER_HOST": "tcp://docker.internal:2375",
		},
		true,
		"",
	)
	envMap := envListToMap(env)

	if got := envMap["DOCKER_HOST"]; got != "tcp://docker.internal:2375" {
		t.Fatalf("expected explicit DOCKER_HOST override to be preserved, got %q", got)
	}
	if _, ok := envMap["HOME"]; ok {
		t.Fatalf("expected HOME to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["XDG_RUNTIME_DIR"]; ok {
		t.Fatalf("expected XDG_RUNTIME_DIR to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["DOCKER_CONTEXT"]; ok {
		t.Fatalf("expected DOCKER_CONTEXT to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["DOCKER_TLS_VERIFY"]; ok {
		t.Fatalf("expected DOCKER_TLS_VERIFY to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["COMPOSE_PROJECT_NAME"]; ok {
		t.Fatalf("expected COMPOSE_PROJECT_NAME to be cleared when parent env inheritance is disabled")
	}
	if _, ok := envMap["COMPOSE_PROFILES"]; ok {
		t.Fatalf("expected COMPOSE_PROFILES to be cleared when parent env inheritance is disabled")
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func TestBuildCommandEnvAllowsPrefixOptInOverrides(t *testing.T) {
	env, blocked := buildCommandEnv(
		nil,
		[]string{"PATH"},
		[]string{"DOCKER_TLS_"},
		map[string]string{
			"DOCKER_TLS_CERTDIR": "/certs",
		},
		true,
		"",
	)
	envMap := envListToMap(env)

	if got := envMap["DOCKER_TLS_CERTDIR"]; got != "/certs" {
		t.Fatalf("expected DOCKER_TLS_CERTDIR override to be preserved, got %q", got)
	}
	if len(blocked) != 0 {
		t.Fatalf("expected no blocked env keys, got %v", blocked)
	}
}

func envListToMap(entries []string) map[string]string {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		result[key] = value
	}
	return result
}
