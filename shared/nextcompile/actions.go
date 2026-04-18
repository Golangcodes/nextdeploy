package nextcompile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Server Actions are Next.js's primary mutation primitive. The compiled
// build emits `.next/server/server-reference-manifest.json` mapping each
// action's opaque ID (a hex hash Next derives from the file path + export
// name) to the compiled module + export that implements it.
//
// At request time, Next's client sends a POST with:
//   - Next-Action: <actionId>  (request header)
//   - the action args (multipart/form-data, urlencoded, or JSON)
//
// actions.mjs at runtime dispatches by looking up the actionId in the
// action manifest this file emits. No Next runtime involvement at
// request time — the manifest carries everything the dispatcher needs.
//
// Manifest shape upstream (abbreviated; Next 14 / 15):
//
//	{
//	  "node":     { "<actionId>": { "workers": { "<moduleId>": "exportName" },
//	                                 "layer":   { "<moduleId>": "action-browser" } } },
//	  "edge":     { "<actionId>": { "workers": {...}, "layer": {...} } },
//	  "encryption": { "key": "..." }
//	}
//
// Our emitted manifest is flattened and stable:
//
//	{
//	  "schemaVersion": "1",
//	  "actions": { "<actionId>": { "module": "<moduleId>", "export": "...", "runtime": "node"|"edge" } }
//	}

const actionManifestSchemaVersion = "1"
const upstreamActionManifestName = "server-reference-manifest.json"

// ActionRuntime is where the action was tagged to run in the upstream
// manifest. We carry it forward so the runtime dispatcher could branch
// if we ever care (today Workers runs everything identically).
type ActionRuntime string

const (
	ActionRuntimeNode ActionRuntime = "node"
	ActionRuntimeEdge ActionRuntime = "edge"
)

// Action is one entry in our flattened manifest.
type Action struct {
	ID      string        `json:"-"`
	Module  string        `json:"module"`
	Export  string        `json:"export"`
	Runtime ActionRuntime `json:"runtime"`
}

// ActionManifest is the on-disk shape emitted to action_manifest.json.
// Keys sort deterministically via encoding/json's map ordering.
type ActionManifest struct {
	SchemaVersion string            `json:"schemaVersion"`
	Actions       map[string]Action `json:"actions"`
}

// ErrNoActionManifest is returned by DetectServerActions when the
// upstream manifest is absent. Expected for apps that don't use
// Server Actions — caller logs at debug and moves on.
var ErrNoActionManifest = errors.New("server-reference-manifest.json not present in standalone tree")

// DetectServerActions reads Next's server-reference-manifest.json from
// the standalone tree and returns a flattened ActionManifest.
//
// Lookup order:
//  1. <standaloneDir>/<distDir>/server/server-reference-manifest.json
//  2. <standaloneDir>/server/server-reference-manifest.json (standalone
//     builds sometimes flatten the tree)
func DetectServerActions(standaloneDir, distDir string) (*ActionManifest, error) {
	if distDir == "" {
		distDir = ".next"
	}
	candidates := []string{
		filepath.Join(standaloneDir, distDir, "server", upstreamActionManifestName),
		filepath.Join(standaloneDir, "server", upstreamActionManifestName),
	}
	var data []byte
	var err error
	for _, c := range candidates {
		data, err = os.ReadFile(c) // #nosec G304
		if err == nil {
			break
		}
	}
	if data == nil {
		return nil, ErrNoActionManifest
	}

	return parseUpstreamManifest(data)
}

// upstreamManifest is the shape we parse out of Next's JSON. We only
// consume the subset we actually need; extra fields pass through ignored.
type upstreamManifest struct {
	Node map[string]upstreamEntry `json:"node"`
	Edge map[string]upstreamEntry `json:"edge"`
}

type upstreamEntry struct {
	// Workers is the canonical field. Some Next minors wrote
	// `workers` only when there was one; always a map.
	Workers map[string]string `json:"workers"`
	// Layer is advisory — we parse it but don't branch on it.
	Layer map[string]string `json:"layer"`
}

func parseUpstreamManifest(data []byte) (*ActionManifest, error) {
	var up upstreamManifest
	if err := json.Unmarshal(data, &up); err != nil {
		return nil, fmt.Errorf("parse server-reference-manifest: %w", err)
	}

	out := &ActionManifest{
		SchemaVersion: actionManifestSchemaVersion,
		Actions:       map[string]Action{},
	}

	flatten := func(src map[string]upstreamEntry, runtime ActionRuntime) {
		for actionID, entry := range src {
			for moduleID, exportName := range entry.Workers {
				// Same action ID may repeat across node + edge when an app
				// uses both runtimes for the same action. Prefer node (the
				// default for most apps); edge only wins when node is absent.
				if existing, ok := out.Actions[actionID]; ok {
					if existing.Runtime == ActionRuntimeNode {
						continue
					}
				}
				out.Actions[actionID] = Action{
					ID:      actionID,
					Module:  moduleID,
					Export:  exportName,
					Runtime: runtime,
				}
				// First worker entry wins per action; Next emits deterministic
				// output but we still pin to the smallest moduleID to keep our
				// manifest stable across pickup order changes.
				break
			}
		}
	}
	flatten(up.Node, ActionRuntimeNode)
	flatten(up.Edge, ActionRuntimeEdge)

	// Stable pass over the map to resolve any per-action ambiguity
	// deterministically — pick the lexicographically smallest moduleID
	// when multiple workers were registered for the same action.
	stabilize(out, up)

	return out, nil
}

// stabilize re-resolves modules by picking the lex-smallest moduleID per
// action. Without this, map iteration order in parseUpstreamManifest
// could pick different modules on different runs.
func stabilize(out *ActionManifest, up upstreamManifest) {
	pick := func(entries map[string]upstreamEntry, runtime ActionRuntime) {
		for actionID, entry := range entries {
			current, ok := out.Actions[actionID]
			if !ok {
				continue
			}
			// Only stabilize entries currently attributed to this runtime.
			if current.Runtime != runtime {
				continue
			}
			var modules []string
			for m := range entry.Workers {
				modules = append(modules, m)
			}
			if len(modules) == 0 {
				continue
			}
			sort.Strings(modules)
			chosen := modules[0]
			out.Actions[actionID] = Action{
				ID:      actionID,
				Module:  chosen,
				Export:  entry.Workers[chosen],
				Runtime: runtime,
			}
		}
	}
	pick(up.Node, ActionRuntimeNode)
	pick(up.Edge, ActionRuntimeEdge)
}

// EmitActionManifest writes the flattened manifest to
// <outDir>/_nextdeploy/action_manifest.json. Returns the final path. When
// the manifest has zero actions, the file is still written (the runtime
// dispatcher handles empty gracefully and its absence would be ambiguous
// — "no actions" vs "file missing" vs "malformed build").
func EmitActionManifest(m *ActionManifest, outDir string) (string, error) {
	if m == nil {
		m = &ActionManifest{SchemaVersion: actionManifestSchemaVersion, Actions: map[string]Action{}}
	}
	dir := filepath.Join(outDir, "_nextdeploy")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir _nextdeploy: %w", err)
	}
	path := filepath.Join(dir, "action_manifest.json")
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal action manifest: %w", err)
	}
	buf = append(buf, '\n')
	if err := os.WriteFile(path, buf, 0o640); err != nil {
		return "", fmt.Errorf("write action manifest: %w", err)
	}
	return path, nil
}
