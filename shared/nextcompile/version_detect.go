package nextcompile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DetectVersions reads Next.js and React versions from the standalone
// build's dependency graph. Lookup order:
//  1. <standaloneDir>/package.json (standalone builds vendor their own)
//  2. <standaloneDir>/node_modules/next/package.json
//  3. <standaloneDir>/../package.json (repo root, last resort)
//
// Returns ErrVersionNotFound when none resolve. Callers should treat
// that as fatal — the runtime bundle selection depends on knowing which
// Next major is in play.
func DetectVersions(standaloneDir string) (NextVersion, ReactVersion, error) {
	var zeroNext NextVersion
	var zeroReact ReactVersion

	nextRaw, reactRaw, err := readDepVersions(standaloneDir)
	if err != nil {
		return zeroNext, zeroReact, err
	}

	nv, err := parseNextVersion(nextRaw)
	if err != nil {
		return zeroNext, zeroReact, fmt.Errorf("parse next version %q: %w", nextRaw, err)
	}

	rv, err := parseReactVersion(reactRaw)
	if err != nil {
		// React version is nice-to-have — not every standalone tree resolves
		// it cleanly. Fall through with an empty struct and a readable Raw.
		rv = ReactVersion{Raw: reactRaw}
	}

	return nv, rv, nil
}

// ErrVersionNotFound is returned when no package.json yields a Next version.
var ErrVersionNotFound = fmt.Errorf("nextcompile: could not locate next.js version in standalone tree")

// readDepVersions walks the lookup order and returns the first (next,react)
// pair where at least `next` is present.
func readDepVersions(standaloneDir string) (string, string, error) {
	candidates := []string{
		filepath.Join(standaloneDir, "package.json"),
		filepath.Join(standaloneDir, "node_modules", "next", "package.json"),
		filepath.Join(filepath.Dir(standaloneDir), "package.json"),
	}

	for _, path := range candidates {
		nv, rv, ok := readPackageJSON(path)
		if ok {
			return nv, rv, nil
		}
	}
	return "", "", ErrVersionNotFound
}

// readPackageJSON returns (nextVersion, reactVersion, ok). Two shapes are
// handled: a direct package (the `version` field — used by node_modules/next)
// and a consumer package (dependencies block — used by the app's own file).
func readPackageJSON(path string) (string, string, bool) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return "", "", false
	}

	var pkg struct {
		Name            string            `json:"name"`
		Version         string            `json:"version"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", "", false
	}

	// Shape 1: this file IS next/package.json.
	if pkg.Name == "next" && pkg.Version != "" {
		return pkg.Version, lookupDep(pkg.Dependencies, "react"), true
	}

	// Shape 2: app/consumer package with a next dependency.
	if v := lookupDep(pkg.Dependencies, "next"); v != "" {
		return v, lookupDep(pkg.Dependencies, "react"), true
	}
	if v := lookupDep(pkg.DevDependencies, "next"); v != "" {
		return v, lookupDep(pkg.DevDependencies, "react"), true
	}

	return "", "", false
}

func lookupDep(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

// parseNextVersion handles the formats observed in real package.json files:
//   - Exact: "14.2.3"
//   - Caret: "^14.2.3"
//   - Tilde: "~14.2.3"
//   - Canary: "15.0.0-canary.42"
//   - RC:     "14.0.0-rc.1"
//
// Anything else (workspace:*, git refs, latest) returns an error. Callers
// should pin Next to a concrete version in production, so this is intentional.
func parseNextVersion(raw string) (NextVersion, error) {
	return parseSemver(raw, "next")
}

func parseReactVersion(raw string) (ReactVersion, error) {
	nv, err := parseSemver(raw, "react")
	return ReactVersion{
		Major: nv.Major,
		Minor: nv.Minor,
		Patch: nv.Patch,
		Raw:   nv.Raw,
	}, err
}

func parseSemver(raw, label string) (NextVersion, error) {
	trimmed := strings.TrimLeft(strings.TrimSpace(raw), "^~>= ")
	if trimmed == "" {
		return NextVersion{Raw: raw}, fmt.Errorf("empty %s version", label)
	}

	// Strip pre-release / build metadata for numeric parsing, but keep Raw intact.
	core := trimmed
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}

	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return NextVersion{Raw: raw}, fmt.Errorf("unexpected %s version format: %s", label, raw)
	}

	out := NextVersion{Raw: raw}
	var err error
	if out.Major, err = strconv.Atoi(parts[0]); err != nil {
		return NextVersion{Raw: raw}, fmt.Errorf("major in %q: %w", raw, err)
	}
	if out.Minor, err = strconv.Atoi(parts[1]); err != nil {
		return NextVersion{Raw: raw}, fmt.Errorf("minor in %q: %w", raw, err)
	}
	if len(parts) >= 3 {
		// Patch may have extra dot segments in pre-release; take the leading int.
		patchDigits := takeLeadingDigits(parts[2])
		if patchDigits != "" {
			out.Patch, _ = strconv.Atoi(patchDigits)
		}
	}
	return out, nil
}

func takeLeadingDigits(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

// RuntimeVariant picks which embedded runtime bundle matches the detected
// Next version. Only majors are consulted — minors share a runtime within
// a major because our JS runtime targets the stable public surface, not
// the NextServer internals that churn per-minor.
func (v NextVersion) RuntimeVariant() string {
	switch {
	case v.Major >= 15:
		return "v15"
	case v.Major == 14:
		return "v14"
	case v.Major == 13:
		return "v13"
	default:
		return "v14" // conservative default — most common in the wild today
	}
}
