package pathutil

import "testing"

func TestResolvePathPreservesPOSIXRootedProjectPath(t *testing.T) {
	t.Parallel()

	resolved, err := ResolvePath("/opt/mtproxy-installer")
	if err != nil {
		t.Fatalf("expected POSIX-rooted path to resolve, got %v", err)
	}
	if resolved != "/opt/mtproxy-installer" {
		t.Fatalf("unexpected resolved path: got %q", resolved)
	}
}

func TestCanonicalPathKeyPreservesPOSIXRootedProjectPath(t *testing.T) {
	t.Parallel()

	key := CanonicalPathKey(`\opt\mtproxy-installer\providers\telemt`)
	if key != "/opt/mtproxy-installer/providers/telemt" {
		t.Fatalf("unexpected canonical key: got %q", key)
	}
}
