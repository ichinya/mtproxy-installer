package runtime

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectProviderFromEnvUsesProviderConfigPath(t *testing.T) {
	t.Parallel()

	installDir := t.TempDir()
	telemtConfigPath := filepath.Join(installDir, "providers", string(ProviderTelemt), "telemt.toml")
	if err := os.MkdirAll(filepath.Dir(telemtConfigPath), 0o755); err != nil {
		t.Fatalf("failed to create telemt provider dir: %v", err)
	}
	if err := os.WriteFile(telemtConfigPath, []byte("# telemt config\n"), 0o600); err != nil {
		t.Fatalf("failed to write telemt config: %v", err)
	}

	envFile := &EnvFile{
		values: map[string]string{
			envProviderKey: string(ProviderTelemt),
		},
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	descriptor, err := DetectProvider(installDir, envFile, logger)
	if err != nil {
		t.Fatalf("expected provider detection success, got %v", err)
	}

	if descriptor.Name != ProviderTelemt {
		t.Fatalf("expected provider telemt, got %q", descriptor.Name)
	}
	if descriptor.Source != ProviderSourceEnv {
		t.Fatalf("expected provider source env, got %q", descriptor.Source)
	}
	if descriptor.ConfigPath != telemtConfigPath {
		t.Fatalf("unexpected provider config path: got %q want %q", descriptor.ConfigPath, telemtConfigPath)
	}

	logText := logs.String()
	if !strings.Contains(logText, "provider detected") {
		t.Fatalf("expected provider detected log, got: %s", logText)
	}
}

func TestDetectProviderReportsMismatchWhenEnvAndConfigConflict(t *testing.T) {
	t.Parallel()

	installDir := t.TempDir()
	mtgConfigPath := filepath.Join(installDir, "providers", string(ProviderMTG), "mtg.conf")
	if err := os.MkdirAll(filepath.Dir(mtgConfigPath), 0o755); err != nil {
		t.Fatalf("failed to create mtg provider dir: %v", err)
	}
	if err := os.WriteFile(mtgConfigPath, []byte("# mtg config\n"), 0o600); err != nil {
		t.Fatalf("failed to write mtg config: %v", err)
	}

	envFile := &EnvFile{
		values: map[string]string{
			envProviderKey: string(ProviderTelemt),
		},
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := DetectProvider(installDir, envFile, logger)
	if err == nil {
		t.Fatalf("expected provider mismatch error")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeProviderMismatch {
		t.Fatalf("expected provider_mismatch code, got %s", runtimeErr.Code)
	}
	if !strings.Contains(runtimeErr.Error(), "runtime provider mismatch") {
		t.Fatalf("expected actionable mismatch message, got: %v", runtimeErr.Error())
	}

	logText := logs.String()
	if !strings.Contains(logText, "provider mismatch detected") {
		t.Fatalf("expected mismatch diagnostics log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR severity for mismatch, got: %s", logText)
	}
}

func TestDetectProviderReportsAmbiguousHeuristicState(t *testing.T) {
	t.Parallel()

	installDir := t.TempDir()
	telemtConfigPath := filepath.Join(installDir, "providers", string(ProviderTelemt), "telemt.toml")
	mtgConfigPath := filepath.Join(installDir, "providers", string(ProviderMTG), "mtg.conf")
	for _, configPath := range []string{telemtConfigPath, mtgConfigPath} {
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatalf("failed to create provider dir: %v", err)
		}
		if err := os.WriteFile(configPath, []byte("# provider config\n"), 0o600); err != nil {
			t.Fatalf("failed to write provider config: %v", err)
		}
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := DetectProvider(installDir, nil, logger)
	if err == nil {
		t.Fatalf("expected ambiguous provider error")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeProviderAmbiguous {
		t.Fatalf("expected provider_ambiguous code, got %s", runtimeErr.Code)
	}

	logText := logs.String()
	if !strings.Contains(logText, "ambiguous provider state") {
		t.Fatalf("expected ambiguous-state log, got: %s", logText)
	}
	if !strings.Contains(logText, "level=ERROR") {
		t.Fatalf("expected ERROR severity for ambiguous provider state, got: %s", logText)
	}
}

func TestDetectProviderRejectsOfficialProviderFromEnv(t *testing.T) {
	t.Parallel()

	installDir := t.TempDir()
	envFile := &EnvFile{
		values: map[string]string{
			envProviderKey: string(ProviderOfficial),
		},
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := DetectProvider(installDir, envFile, logger)
	if err == nil {
		t.Fatalf("expected official provider to be rejected")
	}

	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeProviderUnsupported {
		t.Fatalf("expected provider_unsupported code, got %s", runtimeErr.Code)
	}
	if !strings.Contains(runtimeErr.Error(), "reference-only") {
		t.Fatalf("expected reference-only message, got: %v", runtimeErr.Error())
	}

	logText := logs.String()
	if !strings.Contains(logText, "unsupported provider from env") {
		t.Fatalf("expected unsupported-provider log, got: %s", logText)
	}
}

func TestProviderConfigPathSupportsTelemtAndMTGOnly(t *testing.T) {
	t.Parallel()

	installDir := t.TempDir()

	telemtPath, err := providerConfigPath(installDir, ProviderTelemt)
	if err != nil {
		t.Fatalf("expected telemt config path success, got %v", err)
	}
	if want := filepath.Join(installDir, "providers", "telemt", "telemt.toml"); telemtPath != want {
		t.Fatalf("unexpected telemt config path: got %q want %q", telemtPath, want)
	}

	mtgPath, err := providerConfigPath(installDir, ProviderMTG)
	if err != nil {
		t.Fatalf("expected mtg config path success, got %v", err)
	}
	if want := filepath.Join(installDir, "providers", "mtg", "mtg.conf"); mtgPath != want {
		t.Fatalf("unexpected mtg config path: got %q want %q", mtgPath, want)
	}

	_, err = providerConfigPath(installDir, ProviderOfficial)
	if err == nil {
		t.Fatalf("expected unsupported provider config path error")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected runtime error, got %T", err)
	}
	if runtimeErr.Code != CodeProviderUnsupported {
		t.Fatalf("expected provider_unsupported code, got %s", runtimeErr.Code)
	}
}
