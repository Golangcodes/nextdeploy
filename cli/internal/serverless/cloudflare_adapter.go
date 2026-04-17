package serverless

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared"
)

//go:embed templates/worker_shim.mjs
var workerShimSource []byte

// BuildWorkerBundle adapts a Next.js standalone build into a single
// Cloudflare Worker module bundle.
//
// Pipeline:
//  1. Drop the embedded shim into <standaloneDir>/worker.mjs
//  2. Invoke `npx esbuild` to bundle shim + standalone server into a
//     single ESM module with node:* externals (Workers provides them
//     via nodejs_compat_v2)
//  3. Return the resulting bundle path
//
// Requires Node + npx on PATH. Returns a clear error if missing.
func BuildWorkerBundle(standaloneDir string, log *shared.Logger) (string, error) {
	if _, err := os.Stat(standaloneDir); err != nil {
		return "", fmt.Errorf("standalone dir not found: %w", err)
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return "", fmt.Errorf("npx not found on PATH (install Node.js): %w", err)
	}

	shimPath := filepath.Join(standaloneDir, "_nextdeploy_worker.mjs")
	if err := os.WriteFile(shimPath, workerShimSource, 0o600); err != nil {
		return "", fmt.Errorf("write shim: %w", err)
	}

	outDir := filepath.Join(standaloneDir, ".nextdeploy-cf")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return "", fmt.Errorf("create out dir: %w", err)
	}
	bundlePath := filepath.Join(outDir, "worker.mjs")

	args := []string{
		"--yes",
		"esbuild@latest",
		shimPath,
		"--bundle",
		"--platform=node",
		"--format=esm",
		"--target=esnext",
		"--main-fields=module,main",
		"--conditions=worker,node",
		"--external:node:*",
		"--external:cloudflare:*",
		"--loader:.node=copy",
		"--loader:.json=json",
		"--outfile=" + bundlePath,
	}

	log.Info("Bundling Worker via esbuild...")
	cmd := exec.Command("npx", args...)
	cmd.Dir = standaloneDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("esbuild bundle failed: %w", err)
	}

	info, err := os.Stat(bundlePath)
	if err != nil {
		return "", fmt.Errorf("bundle output missing: %w", err)
	}
	log.Info("Worker bundle ready: %s (%.1f KB)", bundlePath, float64(info.Size())/1024)
	return bundlePath, nil
}
