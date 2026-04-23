package nextcompile

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// embeddedRuntime is the hand-written JS runtime that ships inside every
// compiled bundle. The source tree is the authoritative copy; esbuild is
// invoked later by the adapter to bundle entry + runtime + compiled Next
// modules into one worker.mjs.
//
// Why embed instead of copy at install time: `go install
// github.com/Golangcodes/nextdeploy/cli` produces a self-contained binary
// with no on-disk sibling files. Embedding is the only way to keep that
// property. Embedded size is a few KB — trivial vs. the rest of the binary.
//
//go:embed all:runtime_src
var embeddedRuntime embed.FS

// ExtractRuntime copies the embedded JS runtime into
// <outDir>/_nextdeploy/runtime/. The target directory is created if it
// doesn't exist; existing files are overwritten (every build regenerates).
//
// Returns the list of extracted paths in deterministic order so callers
// can include them in a build summary and hash for reproducibility.
func ExtractRuntime(outDir string) ([]string, error) {
	runtimeDir := filepath.Join(outDir, "_nextdeploy", "runtime")
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir runtime: %w", err)
	}

	var written []string
	err := fs.WalkDir(embeddedRuntime, "runtime_src", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Strip the embed prefix so "runtime_src/dispatcher.mjs" lands as
		// "<runtimeDir>/dispatcher.mjs".
		rel, err := filepath.Rel("runtime_src", p)
		if err != nil {
			return err
		}
		target := filepath.Join(runtimeDir, rel)

		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}

		data, err := embeddedRuntime.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		if err := os.WriteFile(target, data, 0o640); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		written = append(written, target)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// RuntimeSourceFiles returns the list of .mjs files bundled in the
// embedded runtime, sorted. Used for test assertions and the content-hash
// calculation without requiring extraction.
func RuntimeSourceFiles() ([]string, error) {
	var out []string
	err := fs.WalkDir(embeddedRuntime, "runtime_src", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) == ".mjs" {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}
