package nextcompile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNextVersion(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		major   int
		minor   int
		patch   int
		wantErr bool
	}{
		{"exact", "14.2.3", 14, 2, 3, false},
		{"caret", "^15.0.1", 15, 0, 1, false},
		{"tilde", "~13.4.19", 13, 4, 19, false},
		{"canary", "15.0.0-canary.42", 15, 0, 0, false},
		{"rc", "14.0.0-rc.1", 14, 0, 0, false},
		{"two-segment", "14.2", 14, 2, 0, false},
		{"workspace", "workspace:*", 0, 0, 0, true},
		{"empty", "", 0, 0, 0, true},
		{"garbage", "latest", 0, 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseNextVersion(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.in, v)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if v.Major != tc.major || v.Minor != tc.minor || v.Patch != tc.patch {
				t.Errorf("got %d.%d.%d, want %d.%d.%d", v.Major, v.Minor, v.Patch, tc.major, tc.minor, tc.patch)
			}
			if v.Raw != tc.in {
				t.Errorf("Raw not preserved: got %q, want %q", v.Raw, tc.in)
			}
		})
	}
}

func TestRuntimeVariant(t *testing.T) {
	cases := []struct {
		major int
		want  string
	}{
		{13, "v13"},
		{14, "v14"},
		{15, "v15"},
		{16, "v15"}, // newer defaults forward
		{12, "v14"}, // older defaults to safe middle
	}
	for _, tc := range cases {
		got := NextVersion{Major: tc.major}.RuntimeVariant()
		if got != tc.want {
			t.Errorf("major=%d: got %q, want %q", tc.major, got, tc.want)
		}
	}
}

func TestDetectVersions_AppPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{
		"name": "my-app",
		"dependencies": {
			"next": "14.2.3",
			"react": "18.3.1"
		}
	}`)

	nv, rv, err := DetectVersions(dir)
	if err != nil {
		t.Fatalf("DetectVersions: %v", err)
	}
	if nv.Major != 14 || nv.Minor != 2 || nv.Patch != 3 {
		t.Errorf("next: got %+v, want 14.2.3", nv)
	}
	if rv.Major != 18 || rv.Minor != 3 {
		t.Errorf("react: got %+v, want 18.3.x", rv)
	}
}

func TestDetectVersions_NodeModulesNextPackage(t *testing.T) {
	dir := t.TempDir()
	nextDir := filepath.Join(dir, "node_modules", "next")
	if err := os.MkdirAll(nextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nextDir, "package.json"), `{
		"name": "next",
		"version": "15.0.0-canary.42",
		"dependencies": {"react": "19.0.0-rc.0"}
	}`)

	nv, rv, err := DetectVersions(dir)
	if err != nil {
		t.Fatalf("DetectVersions: %v", err)
	}
	if nv.Major != 15 || nv.Raw != "15.0.0-canary.42" {
		t.Errorf("next: got %+v, want 15.0.0-canary.42", nv)
	}
	if rv.Major != 19 {
		t.Errorf("react: got %+v, want major=19", rv)
	}
	if nv.RuntimeVariant() != "v15" {
		t.Errorf("runtime variant: got %q, want v15", nv.RuntimeVariant())
	}
}

func TestDetectVersions_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, err := DetectVersions(dir)
	if err == nil {
		t.Fatal("expected ErrVersionNotFound, got nil")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
