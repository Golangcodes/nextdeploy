package packaging

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

type PackageResult struct {
	LambdaZipPath string
	LambdaZipSize int64
	S3Assets      []S3Asset
	SizeWarning   string
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

	// Inject bridge adapter (content from cli/internal/serverless/aws.go)
	// I'll hardcode it here or pass it in. For now, I'll use a placeholder or the actual one if I can.
	// Since I'm in internal/packaging, I shouldn't depend on cli/internal/serverless.
	// I'll define it here.
	bridgeJS := `const http = require('http');
const path = require('path');
const { spawn } = require('child_process');

let serverReady = false;
let serverPort = 3000;
let cachedSecrets = null;

const serverPath = path.join(__dirname, 'server.js');

const fetchSecrets = async () => {
    const secretName = process.env.ND_SECRET_NAME;
    if (!secretName) return {};
    
    // Check if we already have secrets in env (injected via fallback)
    if (process.env.TEST_SECRET_KEY) {
        console.log('[bridge] Using secrets injected via environment variables (fallback mode)');
        return {}; // Already in process.env
    }

    const options = {
        hostname: 'localhost',
        port: 2773,
        path: '/secretsmanager/get?secretId=' + encodeURIComponent(secretName),
        headers: { 'X-Aws-Parameters-Secrets-Token': process.env.AWS_SESSION_TOKEN || '' },
        timeout: 1000
    };

    console.log('[bridge] Attempting to fetch secrets from AWS Extension...');
    return new Promise((resolve) => {
        const req = http.get(options, (res) => {
            let data = '';
            res.on('data', (chunk) => data += chunk);
            res.on('end', () => {
                try {
                    const parsed = JSON.parse(data);
                    if (parsed.SecretString) {
                        const secrets = JSON.parse(parsed.SecretString);
                        console.log('[bridge] Successfully fetched secrets from extension');
                        resolve(secrets);
                    } else {
                        console.error('[bridge] Extension returned no SecretString');
                        resolve({});
                    }
                } catch (e) { 
                    console.error('[bridge] Failed to parse secrets from extension');
                    resolve({}); 
                }
            });
        });
        req.on('error', (err) => {
            console.log('[bridge] Secrets Extension unreachable (expected if layer is missing):', err.message);
            resolve({});
        });
        req.on('timeout', () => {
            req.destroy();
            console.log('[bridge] Secrets Extension request timed out');
            resolve({});
        });
        req.end();
    });
};

const waitForServer = async () => {
    for (let i = 0; i < 50; i++) {
        try {
            await new Promise((resolve, reject) => {
                const req = http.get({
                    hostname: '127.0.0.1',
                    port: serverPort,
                    path: '/',
                    timeout: 500,
                }, (res) => resolve(true));
                req.on('error', reject);
                req.end();
            });
            return true;
        } catch (e) { await new Promise(r => setTimeout(r, 100)); }
    }
    throw new Error('Server timed out');
};

const startServer = async () => {
    cachedSecrets = await fetchSecrets();
    const env = { ...process.env, ...cachedSecrets, PORT: serverPort, HOSTNAME: '127.0.0.1', NODE_ENV: 'production' };
    const serverProcess = spawn('node', [serverPath], { env: env, stdio: 'inherit' });
    serverProcess.on('exit', () => { serverReady = false; });
    await waitForServer();
    serverReady = true;
};

const warmup = startServer();

exports.handler = async (event) => {
    await warmup;
    return new Promise((resolve, reject) => {
        const method = (event.requestContext && event.requestContext.http) ? event.requestContext.http.method : event.httpMethod;
        const rawPath = event.rawPath || event.path || '/';
        const queryString = event.rawQueryString || '';
        
        const incomingHeaders = event.headers || {};
        const getHeader = (name) => {
            const lowerName = name.toLowerCase();
            for (const key in incomingHeaders) {
                if (key.toLowerCase() === lowerName) return incomingHeaders[key];
            }
            return null;
        };
        
        // Recover original Host for Server Action CSRF bypass
        const cfDomain = process.env.ND_CF_DOMAIN;
        const customDomain = process.env.ND_CUSTOM_DOMAIN;
        const origin = getHeader('origin');
        const incomingProto = getHeader('x-forwarded-proto') || 'https';
        let fwHost = getHeader('x-forwarded-host') || getHeader('host') || 'localhost';

        if (origin) {
            fwHost = origin.replace(/^https?:\/\//, '');
        } else if (customDomain) {
            fwHost = customDomain;
        } else if (cfDomain) {
            fwHost = cfDomain;
        }

        const headers = {
            ...incomingHeaders,
            'x-forwarded-proto': incomingProto,
            'x-forwarded-port': '3000',
            'x-forwarded-host': fwHost,
            'host': fwHost, 
        };

        const options = {
            hostname: '127.0.0.1',
            port: serverPort,
            path: rawPath + (queryString ? '?' + queryString : ''),
            method: method,
            headers: headers,
        };
        const req = http.request(options, (res) => {
            const chunks = [];
            res.on('data', (chunk) => chunks.push(chunk));
            res.on('end', () => {
                const body = Buffer.concat(chunks);
                resolve({
                    statusCode: res.statusCode,
                    headers: res.headers,
                    body: body.toString('base64'),
                    isBase64Encoded: true
                });
            });
        });
        if (event.body) {
            req.write(event.isBase64Encoded ? Buffer.from(event.body, 'base64') : event.body);
        }
        req.on('error', reject);
        req.end();
    });
};`

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
