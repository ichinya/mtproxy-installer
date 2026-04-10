package version

import "testing"

func TestCurrentDefaults(t *testing.T) {
	resetVersionState(t, "", "", "", "")

	info := Current()

	if info.Version != "dev" {
		t.Fatalf("expected default version dev, got %q", info.Version)
	}
	if info.Commit != "unknown" {
		t.Fatalf("expected default commit unknown, got %q", info.Commit)
	}
	if info.BuildDate != "unknown" {
		t.Fatalf("expected default build date unknown, got %q", info.BuildDate)
	}
	if info.BuildMode != "development" {
		t.Fatalf("expected default build mode development, got %q", info.BuildMode)
	}
	if !info.IsDevelopment() {
		t.Fatalf("expected dev mode to be true")
	}
}

func TestCurrentInfersProductionMode(t *testing.T) {
	resetVersionState(t, "1.2.3", "abcd1234", "2026-04-10T00:00:00Z", "")

	info := Current()

	if info.BuildMode != "production" {
		t.Fatalf("expected inferred production mode, got %q", info.BuildMode)
	}
	if info.IsDevelopment() {
		t.Fatalf("expected release build to be non-development")
	}
}

func TestInfoStartupMode(t *testing.T) {
	cases := []struct {
		name     string
		info     Info
		expected string
	}{
		{
			name:     "development mode",
			info:     Info{Version: "dev", BuildMode: "development"},
			expected: "development",
		},
		{
			name:     "production mode",
			info:     Info{Version: "1.0.0", BuildMode: "production"},
			expected: "production",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.StartupMode(); got != tc.expected {
				t.Fatalf("expected startup mode %q, got %q", tc.expected, got)
			}
		})
	}
}

func resetVersionState(t *testing.T, ver string, commit string, buildDate string, buildMode string) {
	t.Helper()

	oldVersion := Version
	oldCommit := Commit
	oldBuildDate := BuildDate
	oldBuildMode := BuildMode

	Version = ver
	Commit = commit
	BuildDate = buildDate
	BuildMode = buildMode

	t.Cleanup(func() {
		Version = oldVersion
		Commit = oldCommit
		BuildDate = oldBuildDate
		BuildMode = oldBuildMode
	})
}
