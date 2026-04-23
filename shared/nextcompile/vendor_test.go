package nextcompile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVendorRSC_ESMProduction(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "out")

	pkgDir := filepath.Join(dir, "node_modules", "react-server-dom-webpack")
	writeFile(t, filepath.Join(pkgDir, "package.json"),
		`{"name":"react-server-dom-webpack","version":"18.3.1"}`)
	writeFile(t, filepath.Join(pkgDir, "esm", "react-server-dom-webpack-server.edge.production.js"),
		`// react 18.3.1 server.edge production build
export function renderToReadableStream(){}`)

	got, err := VendorRSC(dir, bundleDir)
	if err != nil {
		t.Fatalf("VendorRSC: %v", err)
	}
	if got.Version != "18.3.1" {
		t.Errorf("version: got %s", got.Version)
	}
	if got.BuildKind != "production" {
		t.Errorf("buildKind: got %s", got.BuildKind)
	}
	if got.Bytes == 0 {
		t.Error("zero bytes written")
	}

	// Target file must exist at the expected path.
	expPath := filepath.Join(bundleDir, "_nextdeploy", "runtime", "vendor",
		"react-server-dom-webpack", "server.edge.mjs")
	if got.TargetPath != expPath {
		t.Errorf("target path: got %s, want %s", got.TargetPath, expPath)
	}
	data, err := os.ReadFile(expPath) // #nosec G304
	if err != nil {
		t.Fatalf("vendored file unreadable: %v", err)
	}
	if len(data) == 0 {
		t.Error("vendored file is empty")
	}
}

func TestVendorRSC_PrefersProductionOverDev(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "out")

	pkgDir := filepath.Join(dir, "node_modules", "react-server-dom-webpack")
	writeFile(t, filepath.Join(pkgDir, "package.json"),
		`{"name":"react-server-dom-webpack","version":"19.0.0"}`)
	// Both flavors present.
	writeFile(t, filepath.Join(pkgDir, "esm", "react-server-dom-webpack-server.edge.production.js"),
		`// prod`)
	writeFile(t, filepath.Join(pkgDir, "esm", "react-server-dom-webpack-server.edge.development.js"),
		`// dev`)

	got, err := VendorRSC(dir, bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.BuildKind != "production" {
		t.Errorf("expected production, got %s", got.BuildKind)
	}

	data, _ := os.ReadFile(got.TargetPath) // #nosec G304
	if string(data) != "// prod" {
		t.Errorf("wrong build vendored: %q", data)
	}
}

func TestVendorRSC_FallsBackToDevThenLegacy(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "out")

	pkgDir := filepath.Join(dir, "node_modules", "react-server-dom-webpack")
	writeFile(t, filepath.Join(pkgDir, "package.json"),
		`{"name":"react-server-dom-webpack","version":"18.0.0"}`)
	// Only the legacy flat file exists.
	writeFile(t, filepath.Join(pkgDir, "server.edge.js"), `// legacy`)

	got, err := VendorRSC(dir, bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.BuildKind != "legacy" {
		t.Errorf("expected legacy, got %s", got.BuildKind)
	}
}

func TestVendorRSC_AppRootFallback(t *testing.T) {
	// Standalone tree lacks node_modules but the app root above it has it.
	// pnpm-workspace-style layouts hit this path.
	root := t.TempDir()
	standalone := filepath.Join(root, ".next", "standalone")
	if err := os.MkdirAll(standalone, 0o755); err != nil {
		t.Fatal(err)
	}

	pkgDir := filepath.Join(root, "node_modules", "react-server-dom-webpack")
	writeFile(t, filepath.Join(pkgDir, "package.json"),
		`{"name":"react-server-dom-webpack","version":"18.3.1"}`)
	writeFile(t, filepath.Join(pkgDir, "esm", "react-server-dom-webpack-server.edge.production.js"),
		`// root-level`)

	got, err := VendorRSC(standalone, filepath.Join(root, "out"))
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	data, _ := os.ReadFile(got.TargetPath) // #nosec G304
	if string(data) != "// root-level" {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestVendorRSC_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := VendorRSC(dir, filepath.Join(dir, "out"))
	if !errors.Is(err, ErrRSCPackageNotFound) {
		t.Errorf("expected ErrRSCPackageNotFound, got %v", err)
	}
}

func TestVendorRSC_PackageWithoutServerEdge(t *testing.T) {
	// Package is installed but no server.edge file — corrupt or unexpected
	// publish. We treat this as non-recoverable and surface the exact dir.
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "react-server-dom-webpack")
	writeFile(t, filepath.Join(pkgDir, "package.json"),
		`{"name":"react-server-dom-webpack","version":"99.0.0"}`)
	// Intentionally no server.edge build present.

	_, err := VendorRSC(dir, filepath.Join(dir, "out"))
	if err == nil {
		t.Fatal("expected error for missing server.edge")
	}
	if errors.Is(err, ErrRSCPackageNotFound) {
		t.Errorf("should NOT be ErrRSCPackageNotFound — package exists, build is missing")
	}
}
