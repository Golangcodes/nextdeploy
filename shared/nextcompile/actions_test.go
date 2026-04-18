package nextcompile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectServerActions_NodeOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".next", "server", upstreamActionManifestName),
		`{
			"node": {
				"abc123": { "workers": { "app/actions/page": "createPost" } },
				"def456": { "workers": { "app/dashboard/page": "deleteItem" } }
			},
			"edge": {},
			"encryption": { "key": "deadbeef" }
		}`)

	m, err := DetectServerActions(dir, ".next")
	if err != nil {
		t.Fatalf("DetectServerActions: %v", err)
	}
	if got := len(m.Actions); got != 2 {
		t.Errorf("action count: got %d, want 2", got)
	}
	if m.Actions["abc123"].Export != "createPost" {
		t.Errorf("abc123 export: %+v", m.Actions["abc123"])
	}
	if m.Actions["abc123"].Runtime != ActionRuntimeNode {
		t.Errorf("abc123 runtime: got %s", m.Actions["abc123"].Runtime)
	}
}

func TestDetectServerActions_NodePreferredOverEdge(t *testing.T) {
	dir := t.TempDir()
	// Same action ID in both runtimes — node must win.
	writeFile(t, filepath.Join(dir, ".next", "server", upstreamActionManifestName),
		`{
			"node": { "shared1": { "workers": { "mod-node": "fn" } } },
			"edge": { "shared1": { "workers": { "mod-edge": "fn" } } }
		}`)

	m, err := DetectServerActions(dir, ".next")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Actions["shared1"]
	if got.Runtime != ActionRuntimeNode {
		t.Errorf("expected node runtime to win, got %s", got.Runtime)
	}
	if got.Module != "mod-node" {
		t.Errorf("expected node module, got %s", got.Module)
	}
}

func TestDetectServerActions_EdgeOnlyWhenNoNode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".next", "server", upstreamActionManifestName),
		`{
			"node": {},
			"edge": { "only-edge": { "workers": { "mod-edge": "hit" } } }
		}`)

	m, err := DetectServerActions(dir, ".next")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Actions["only-edge"]
	if got.Runtime != ActionRuntimeEdge {
		t.Errorf("expected edge runtime, got %s", got.Runtime)
	}
	if got.Module != "mod-edge" {
		t.Errorf("module: %s", got.Module)
	}
}

func TestDetectServerActions_Stable(t *testing.T) {
	// Multiple workers per action — pick the lex-smallest module. Run twice
	// to confirm the choice doesn't depend on map iteration order.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".next", "server", upstreamActionManifestName),
		`{
			"node": {
				"multi": { "workers": { "zzz-mod": "alpha", "aaa-mod": "alpha", "mmm-mod": "alpha" } }
			},
			"edge": {}
		}`)

	for i := 0; i < 5; i++ {
		m, err := DetectServerActions(dir, ".next")
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Actions["multi"].Module; got != "aaa-mod" {
			t.Errorf("run %d picked %q, want aaa-mod", i, got)
		}
	}
}

func TestDetectServerActions_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := DetectServerActions(dir, ".next")
	if !errors.Is(err, ErrNoActionManifest) {
		t.Errorf("expected ErrNoActionManifest, got %v", err)
	}
}

func TestDetectServerActions_StandaloneFlatLayout(t *testing.T) {
	// Some standalone builds flatten server/ to the top — test that fallback.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "server", upstreamActionManifestName),
		`{"node":{"flat":{"workers":{"m":"fn"}}},"edge":{}}`)

	m, err := DetectServerActions(dir, ".next")
	if err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if _, ok := m.Actions["flat"]; !ok {
		t.Error("action missing from flat-layout read")
	}
}

func TestEmitActionManifest_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &ActionManifest{
		SchemaVersion: actionManifestSchemaVersion,
		Actions: map[string]Action{
			"a1": {ID: "a1", Module: "mod-1", Export: "doThing", Runtime: ActionRuntimeNode},
		},
	}
	path, err := EmitActionManifest(m, dir)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "_nextdeploy", "action_manifest.json")
	if path != expected {
		t.Errorf("path: got %s, want %s", path, expected)
	}

	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	var back ActionManifest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	if back.Actions["a1"].Export != "doThing" {
		t.Errorf("round trip lost export: %+v", back.Actions)
	}
	if back.Actions["a1"].Runtime != ActionRuntimeNode {
		t.Errorf("round trip lost runtime: %+v", back.Actions)
	}
}

func TestEmitActionManifest_EmptyStillWritten(t *testing.T) {
	dir := t.TempDir()
	path, err := EmitActionManifest(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	var back ActionManifest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(back.Actions) != 0 {
		t.Errorf("expected empty actions, got %+v", back.Actions)
	}
	if back.SchemaVersion != actionManifestSchemaVersion {
		t.Errorf("schema version missing: %s", back.SchemaVersion)
	}
}
