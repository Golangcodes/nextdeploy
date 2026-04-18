package nextcompile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrRSCPackageNotFound signals that react-server-dom-webpack is not
// present in the standalone tree's node_modules. Callers match on this
// via errors.Is to branch:
//   - manifest.Features.RSC == true → surface to user with vendoring steps
//   - manifest.Features.RSC == false → silently skip, rsc.mjs will 501
//     anyway if ever reached
var ErrRSCPackageNotFound = errors.New("react-server-dom-webpack not found in node_modules")

// VendoredPackage records what VendorRSC copied into the bundle. The
// adapter logs this and includes it in CompileStats.
type VendoredPackage struct {
	Name       string
	Version    string
	SourcePath string
	TargetPath string
	Bytes      int64
	BuildKind  string // "production" | "development" | "legacy"
}

// VendorRSC resolves react-server-dom-webpack from the standalone tree's
// node_modules and copies its server.edge ESM bundle into
// <bundleDir>/_nextdeploy/runtime/vendor/react-server-dom-webpack/server.edge.mjs.
//
// Lookup order (first hit wins):
//  1. <standaloneDir>/node_modules/react-server-dom-webpack
//  2. <standaloneDir>/../node_modules/react-server-dom-webpack    (app root)
//
// Returns ErrRSCPackageNotFound when neither location resolves. The CF
// Workers runtime has no npm at request time, so vendoring is the only
// way Server Components render without OpenNext.
func VendorRSC(standaloneDir, bundleDir string) (*VendoredPackage, error) {
	pkgDir, err := locateRSCPackage(standaloneDir)
	if err != nil {
		return nil, err
	}

	meta, err := readRSCPackageMeta(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("read react-server-dom-webpack metadata: %w", err)
	}

	sourcePath, buildKind, err := findRSCServerEdge(pkgDir)
	if err != nil {
		return nil, err
	}

	targetDir := filepath.Join(bundleDir, "_nextdeploy", "runtime", "vendor", "react-server-dom-webpack")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir vendor: %w", err)
	}
	targetPath := filepath.Join(targetDir, "server.edge.mjs")

	n, err := copyFile(sourcePath, targetPath)
	if err != nil {
		return nil, fmt.Errorf("copy %s → %s: %w", sourcePath, targetPath, err)
	}

	return &VendoredPackage{
		Name:       meta.Name,
		Version:    meta.Version,
		SourcePath: sourcePath,
		TargetPath: targetPath,
		Bytes:      n,
		BuildKind:  buildKind,
	}, nil
}

// locateRSCPackage walks upward from standaloneDir looking for a
// node_modules/react-server-dom-webpack. Next's standalone build lands at
// <project>/.next/standalone, so the package may be two levels up in a
// workspace or monorepo. Cap the walk at 5 levels to avoid unbounded
// upward search in pathological filesystems.
//
// Symlink-transparent by design — pnpm's flat .pnpm store resolves
// because os.Stat follows symlinks.
func locateRSCPackage(standaloneDir string) (string, error) {
	current := standaloneDir
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(current, "node_modules", "react-server-dom-webpack")
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break // hit root
		}
		current = parent
	}
	return "", ErrRSCPackageNotFound
}

type rscPackageMeta struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func readRSCPackageMeta(pkgDir string) (rscPackageMeta, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json")) // #nosec G304
	if err != nil {
		return rscPackageMeta{}, err
	}
	var m rscPackageMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return rscPackageMeta{}, err
	}
	return m, nil
}

// findRSCServerEdge tries the known on-disk layouts for the package and
// returns the first existing file along with its build flavor.
//
// Ordering rationale: prefer ESM production (smallest, no dev warnings),
// fall back to ESM development, then the legacy flat CJS layout as a
// last resort. React 18 published both ESM and CJS; React 19 is ESM-only
// but the file names are stable.
func findRSCServerEdge(pkgDir string) (string, string, error) {
	candidates := []struct {
		rel       string
		buildKind string
	}{
		{"esm/react-server-dom-webpack-server.edge.production.js", "production"},
		{"server.edge.production.js", "production"},
		{"esm/react-server-dom-webpack-server.edge.development.js", "development"},
		{"server.edge.development.js", "development"},
		{"server.edge.js", "legacy"},
	}
	for _, c := range candidates {
		p := filepath.Join(pkgDir, c.rel)
		if _, err := os.Stat(p); err == nil {
			return p, c.buildKind, nil
		}
	}
	return "", "", fmt.Errorf("no server.edge build found in %s (tried esm/ and legacy layouts)", pkgDir)
}

// copyFile byte-copies src to dst and returns the number of bytes written.
// Uses 0640 on the target — worker bundles contain code that can reference
// secrets via bindings, so conservative permissions are correct.
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src) // #nosec G304
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640) // #nosec G304
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, in)
}
