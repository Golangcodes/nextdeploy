package packaging

import (
	"archive/zip"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// bridgeJS is the Node.js shim that the Lambda runtime executes. It:
//   - Fetches secrets from the AWS Parameters & Secrets Lambda Extension
//     (localhost:2773) and merges them into process.env before spawning
//     `node server.js`.
//   - Forwards the Lambda proxy event to the Next.js server over HTTP.
//
// Source of truth is `runtime/bridge.js` — edit there, not here.
//
//go:embed runtime/bridge.js
var bridgeJS string

type PackageResult struct {
	LambdaZipPath string
	LambdaZipSize int64
	// StandaloneTarPath is the provider-agnostic artifact: a gzipped tar of
	// the raw Next.js standalone directory, with no target-specific shims
	// baked in. AWS ignores this (it deploys LambdaZipPath directly);
	// Cloudflare extracts it before running its adapter. Empty if tar
	// creation failed (a warning is logged but packaging does not fail —
	// providers that need it will surface a clear error themselves).
	StandaloneTarPath string
	StandaloneTarSize int64
	S3Assets          []S3Asset
	SizeWarning       string
}

type S3Asset struct {
	LocalPath    string
	S3Key        string
	CacheControl string
	ContentType  string
}

const (
	lambdaZipWarnThresholdBytes = 200 * 1024 * 1024
	lambdaZipHardLimitBytes     = 250 * 1024 * 1024
)

type Packager struct {
	projectRoot   string
	buildDir      string
	standaloneDir string
	publicDir     string
	payload       *nextcore.NextCorePayload
	tmpDir        string
}

func NewPackager(projectRoot string, payload *nextcore.NextCorePayload) (*Packager, error) {
	buildDir := filepath.Join(projectRoot, payload.DistDir)
	standaloneDir := filepath.Join(buildDir, "standalone")

	if _, err := os.Stat(standaloneDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("standalone directory not found at %s", standaloneDir)
	}

	tmpDir, err := os.MkdirTemp("", "nextdeploy-pkg-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	return &Packager{
		projectRoot:   projectRoot,
		buildDir:      buildDir,
		standaloneDir: standaloneDir,
		publicDir:     filepath.Join(projectRoot, "public"),
		payload:       payload,
		tmpDir:        tmpDir,
	}, nil
}

func (p *Packager) Cleanup() { os.RemoveAll(p.tmpDir) }

func (p *Packager) Package() (*PackageResult, error) {
	result := &PackageResult{}

	s3Assets, err := p.collectS3Assets()
	if err != nil {
		return nil, err
	}
	result.S3Assets = s3Assets

	zipPath := filepath.Join(p.tmpDir, "lambda.zip")
	size, err := p.buildLambdaZip(zipPath)
	if err != nil {
		return nil, err
	}

	result.LambdaZipPath = zipPath
	result.LambdaZipSize = size

	if size > lambdaZipHardLimitBytes {
		return nil, fmt.Errorf("lambda zip is %.1fMB — exceeds Lambda's 250MB unzipped limit", float64(size)/(1024*1024))
	}
	if size > lambdaZipWarnThresholdBytes {
		result.SizeWarning = fmt.Sprintf("lambda zip is %.1fMB — approaching Lambda's 250MB limit", float64(size)/(1024*1024))
	}

	tarPath := filepath.Join(p.tmpDir, "app.tar.gz")
	if err := shared.CreateTarGz(p.standaloneDir, tarPath); err != nil {
		// Non-fatal: AWS doesn't need it. Leave the fields zero-valued so
		// Cloudflare's DeployCompute falls back to reading standalone
		// from disk and can decide how loudly to complain.
		fmt.Fprintf(os.Stderr, "warning: could not tar standalone dir for portable artifact: %v\n", err)
	} else if info, err := os.Stat(tarPath); err == nil {
		result.StandaloneTarPath = tarPath
		result.StandaloneTarSize = info.Size()
	}

	return result, nil
}

func (p *Packager) collectS3Assets() ([]S3Asset, error) {
	var assets []S3Asset

	// 1. public/
	if _, err := os.Stat(p.publicDir); err == nil {
		_ = filepath.Walk(p.publicDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(p.publicDir, path)
			assets = append(assets, S3Asset{
				LocalPath:    path,
				S3Key:        rel,
				CacheControl: cacheControlForPublicFile(rel),
				ContentType:  mimeForExt(filepath.Ext(path)),
			})
			return nil
		})
	}

	// 2. .next/static/
	staticDir := filepath.Join(p.buildDir, "static")
	if _, err := os.Stat(staticDir); err == nil {
		_ = filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(p.buildDir, path)
			assets = append(assets, S3Asset{
				LocalPath:    path,
				S3Key:        filepath.Join("_next", rel),
				CacheControl: "public, max-age=31536000, immutable",
				ContentType:  mimeForExt(filepath.Ext(path)),
			})
			return nil
		})
	}

	// 3. Prerendered routes
	for _, route := range p.payload.RouteInfo.StaticRoutes {
		p.addPrerenderedAsset(&assets, route)
	}
	for route := range p.payload.RouteInfo.ISRRoutes {
		p.addPrerenderedAsset(&assets, route)
	}

	// 4. ISR Tag Map (if enabled/built)
	tagMapPath := filepath.Join(p.projectRoot, ".nextdeploy", "assets", "isr-tag-map.json")
	if _, err := os.Stat(tagMapPath); err == nil {
		assets = append(assets, S3Asset{
			LocalPath:    tagMapPath,
			S3Key:        "isr-tag-map.json",
			CacheControl: "public, max-age=0, must-revalidate",
			ContentType:  "application/json",
		})
	}

	return assets, nil
}

func (p *Packager) addPrerenderedAsset(assets *[]S3Asset, routePath string) {
	// Root route "/" maps to index.html
	s3KeyBase := strings.TrimPrefix(routePath, "/")
	if s3KeyBase == "" {
		s3KeyBase = "index"
	}

	// Standalone output structure: .next/standalone/.next/server/
	standaloneNext := filepath.Join(p.standaloneDir, ".next")

	prefixes := []string{
		filepath.Join(standaloneNext, "server", "app"),
		filepath.Join(standaloneNext, "server", "pages"),
	}

	for _, prefix := range prefixes {
		serverPath := filepath.Join(prefix, routePath)

		htmlPath := serverPath + ".html"
		if _, err := os.Stat(htmlPath); err == nil {
			*assets = append(*assets, S3Asset{
				LocalPath:    htmlPath,
				S3Key:        s3KeyBase + ".html",
				CacheControl: "public, max-age=0, must-revalidate",
				ContentType:  "text/html; charset=utf-8",
			})
		}

		rscPath := serverPath + ".rsc"
		if _, err := os.Stat(rscPath); err == nil {
			*assets = append(*assets, S3Asset{
				LocalPath:    rscPath,
				S3Key:        s3KeyBase + ".rsc",
				CacheControl: "public, max-age=0, must-revalidate",
				ContentType:  "text/x-component",
			})
		}
	}
}

func (p *Packager) buildLambdaZip(zipPath string) (int64, error) {
	f, err := os.Create(zipPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	_ = filepath.Walk(p.standaloneDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(p.standaloneDir, path)
		if shouldExcludeFromLambda(rel) {
			return nil
		}

		return addToZip(w, path, rel)
	})

	_ = addBytesToZip(w, "bridge.js", []byte(bridgeJS))
	_ = w.Close()

	info, _ := f.Stat()
	return info.Size(), nil
}

func shouldExcludeFromLambda(relPath string) bool {
	if strings.HasPrefix(relPath, ".next/static/") {
		return true
	}
	if strings.HasSuffix(relPath, ".html") && strings.HasPrefix(relPath, ".next/server/") {
		return true
	}
	if strings.HasSuffix(relPath, ".rsc") && strings.HasPrefix(relPath, ".next/server/") {
		return true
	}
	if strings.HasSuffix(relPath, ".js.map") {
		return true
	}
	if strings.Contains(relPath, "/.next/cache/") {
		return true
	}
	return false
}

func addToZip(w *zip.Writer, path, relPath string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = relPath
	header.Method = zip.Deflate
	writer, err := w.CreateHeader(header)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	return err
}

func addBytesToZip(w *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	writer, err := w.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}
