package serverless

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Golangcodes/nextdeploy/shared/config"
)

// TestLoadLocalSecrets_Precedence verifies the merge order:
//
//	.env  <  cfg.Secrets.Files  <  managed JSON store
//
// This is the regression net for the YAML-merge fix in deploy.go.
func TestLoadLocalSecrets_Precedence(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	// 1. Project-root .env (lowest precedence)
	writeFile(t, ".env", "FOO=from_dotenv\nSHARED=dotenv_loses\nDOTENV_ONLY=1\n")

	// 2. YAML-declared file (middle precedence)
	yamlFile := filepath.Join(dir, "secrets.env")
	writeFile(t, yamlFile, "BAR=from_yaml\nSHARED=yaml_loses\nYAML_ONLY=1\n")

	// 3. Managed JSON store (highest precedence)
	if err := os.MkdirAll(".nextdeploy", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, ".nextdeploy/.env", `{
  "BAZ": {"value": "from_managed", "is_encrypted": false},
  "SHARED": {"value": "managed_wins", "is_encrypted": false},
  "MANAGED_ONLY": {"value": "1", "is_encrypted": false}
}`)

	cfg := &config.NextDeployConfig{
		Secrets: config.SecretsConfig{
			Files: []config.SecretFile{{Path: yamlFile}},
		},
	}

	got, err := loadLocalSecrets(cfg)
	if err != nil {
		t.Fatalf("loadLocalSecrets: %v", err)
	}

	// Precedence checks
	want := map[string]string{
		"FOO":          "from_dotenv",
		"BAR":          "from_yaml",
		"BAZ":          "from_managed",
		"SHARED":       "managed_wins",
		"DOTENV_ONLY":  "1",
		"YAML_ONLY":    "1",
		"MANAGED_ONLY": "1",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
}

// TestLoadLocalSecrets_NoSources returns an empty map without error.
func TestLoadLocalSecrets_NoSources(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	got, err := loadLocalSecrets(&config.NextDeployConfig{})
	if err != nil {
		t.Fatalf("loadLocalSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

// TestLoadLocalSecrets_MissingYamlFile fails loudly (no silent skip).
func TestLoadLocalSecrets_MissingYamlFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := &config.NextDeployConfig{
		Secrets: config.SecretsConfig{
			Files: []config.SecretFile{{Path: "does/not/exist.env"}},
		},
	}
	_, err := loadLocalSecrets(cfg)
	if err == nil {
		t.Fatal("expected error for missing YAML-declared file, got nil")
	}
}

func TestSecretsEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"empty equal", map[string]string{}, map[string]string{}, true},
		{"same content", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"len differs", map[string]string{"k": "v"}, map[string]string{"k": "v", "x": "y"}, false},
		{"value differs", map[string]string{"k": "v"}, map[string]string{"k": "w"}, false},
		{"key missing", map[string]string{"k": "v"}, map[string]string{"x": "v"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := secretsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// chdir switches CWD for the duration of a test, restoring on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
