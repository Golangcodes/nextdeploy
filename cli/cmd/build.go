package cmd

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"container/heap"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"

	"github.com/spf13/cobra"
)

const (
	maxInFlight        = 64
	largeFileThreshold = 4 * 1024 * 1024 // 4MB
)

var workerCount = func() int {
	n := runtime.NumCPU()
	if n > 8 {
		return 8
	}
	return n
}()

var webAllowedExts = map[string]struct{}{
	".js": {}, ".jsx": {}, ".ts": {}, ".tsx": {}, ".mjs": {}, ".cjs": {},
	".css": {}, ".scss": {}, ".sass": {}, ".less": {},
	".html": {}, ".htm": {}, ".xml": {}, ".json": {}, ".yaml": {}, ".yml": {}, ".toml": {},
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".avif": {},
	".svg": {}, ".ico": {}, ".bmp": {}, ".tiff": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".mp4": {}, ".webm": {}, ".ogg": {}, ".ogv": {}, ".mov": {},
	".mp3": {}, ".wav": {}, ".aac": {}, ".opus": {},
	".pdf": {}, ".txt": {}, ".md": {}, ".mdx": {},
	".csv": {}, ".geojson": {},
	".webmanifest": {}, ".map": {},
	"": {},
}

var excludedDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	".nextdeploy":  {},
	"cypress":      {},
	"__tests__":    {},
	"coverage":     {},
	".turbo":       {},
	".vercel":      {},
}

func isTempTarball(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar")
}

func shouldExcludeDir(name string) bool {
	_, ok := excludedDirs[name]
	return ok
}

func shouldExcludeFile(name, relPath string) (bool, string) {
	if isTempTarball(name) {
		return true, "tarball"
	}
	if strings.Contains(relPath, ".env.") {
		return true, ".env file"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := webAllowedExts[ext]; !ok {
		return true, "ext '" + ext + "' not in whitelist"
	}
	return false, ""
}

type fileJob struct {
	index   int
	path    string
	relPath string
	size    int64
}

type fileResult struct {
	job  fileJob
	info os.FileInfo
	data []byte
	err  error
}

type resultHeap []fileResult

func (h resultHeap) Len() int            { return len(h) }
func (h resultHeap) Less(i, j int) bool  { return h[i].job.index < h[j].job.index }
func (h resultHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *resultHeap) Push(x interface{}) { *h = append(*h, x.(fileResult)) }
func (h *resultHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

var (
	forceBuild bool
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the Next.js app and prepare a deployable tarball",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("build", "BUILD")
		log.Info("Starting NextDeploy build process...")

		if forceBuild {
			log.Info("Force build enabled. Bypassing incremental build check...")
		} else if err := nextcore.ValidateBuildState(); err == nil {
			log.Info("Git commit unchanged. Skipping build (Incremental build state matched).")
			os.Exit(0)
		} else {
			log.Info("Build state validation returned: %v. Proceeding with full build.", err)
		}

		payload, err := nextcore.GenerateMetadata()
		if err != nil {
			log.Error("Failed to generate metadata: %v", err)
			os.Exit(1)
		}
		log.Info("Build mode: %s", payload.OutputMode)
		log.Info("Dist directory: %s", payload.DistDir)
		log.Info("Export directory: %s", payload.ExportDir)

		releaseDir := ""
		switch payload.OutputMode {
		case nextcore.OutputModeStandalone:
			releaseDir = filepath.Join(payload.DistDir, "standalone")
			log.Info("Copying public/ → %s/public/...", releaseDir)
			if err := copyDir("public", filepath.Join(releaseDir, "public")); err != nil {
				log.Error("Failed to copy public/: %v", err)
				os.Exit(1)
			}
			log.Info("Copying %s/static/ → %s/%s/static/...", payload.DistDir, releaseDir, payload.DistDir)
			if err := copyDir(filepath.Join(payload.DistDir, "static"), filepath.Join(releaseDir, payload.DistDir, "static")); err != nil {
				log.Error("Failed to copy %s/static/: %v", payload.DistDir, err)
				os.Exit(1)
			}
			log.Info("Copying deployment metadata...")
			if err := copyFile(".nextdeploy/metadata.json", filepath.Join(releaseDir, "metadata.json")); err != nil {
				log.Error("Failed to copy metadata.json: %v", err)
				os.Exit(1)
			}
		case nextcore.OutputModeExport:
			releaseDir = payload.ExportDir
			if err := copyFile(".nextdeploy/metadata.json", filepath.Join(releaseDir, "metadata.json")); err != nil {
				log.Error("Failed to copy metadata.json: %v", err)
				os.Exit(1)
			}
		default:
			releaseDir = "."
			log.Info("Copying deployment metadata for default mode...")
			if err := copyFile(".nextdeploy/metadata.json", "metadata.json"); err != nil {
				log.Error("Failed to copy metadata.json: %v", err)
				os.Exit(1)
			}
		}

		log.Info("Release directory: %s", releaseDir)

		tarballName := "app.tar.gz"
		log.Info("Creating tarball: %s", tarballName)

		if err := createTarball(releaseDir, tarballName, &payload, log); err != nil {
			log.Error("Failed to create tarball: %v", err)
			os.Exit(1)
		}

		log.Info("Build complete! Artifact: %s", tarballName)
	},
}

func init() {
	buildCmd.Flags().BoolVarP(&forceBuild, "force", "f", false, "Force a full build even if git commit is unchanged")
	rootCmd.AddCommand(buildCmd)
}

type logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

func createTarball(sourceDir, targetTar string, payload *nextcore.NextCorePayload, log logger) error {
	outputMode := payload.OutputMode
	log.Info("[tarball] Starting — source=%s target=%s mode=%s workers=%d",
		sourceDir, targetTar, outputMode, workerCount)

	tarfile, err := os.CreateTemp(filepath.Dir(targetTar), "next-deploy-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempName := tarfile.Name()
	log.Info("[tarball] Temp file: %s", tempName)

	success := false
	defer func() {
		_ = tarfile.Close()
		if !success {
			log.Info("[tarball] Removing temp file after error: %s", tempName)
			_ = os.Remove(tempName)
		}
	}()

	bufWriter := bufio.NewWriterSize(tarfile, 512*1024)
	gzw, err := gzip.NewWriterLevel(bufWriter, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	tw := tar.NewWriter(gzw)
	log.Info("[tarball] Phase 1: Walking %s...", sourceDir)
	walkStart := time.Now()

	var jobs []fileJob
	var dirHeaders []tar.Header

	err = filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				log.Info("[tarball] Skip (permission denied): %s", path)
				return nil
			}
			return err
		}
		if isTempTarball(filepath.Base(path)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("rel path for %s: %w", path, err)
		}
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			// Dynamic exclusion of build/export dirs
			if d.Name() == ".git" || d.Name() == ".nextdeploy" {
				log.Info("[tarball] Skip dir (excluded): %s", relPath)
				return filepath.SkipDir
			}

			// Exclude the configured distDir and exportDir from the source walk
			if d.Name() == payload.DistDir || d.Name() == payload.ExportDir {
				// But only if we are walking the ROOT of the project (sourceDir == ".")
				// If sourceDir is "out", we are already inside it, and we shouldn't skip it.
				// However, the walk starts at sourceDir, so d.Name() will be children.
				if sourceDir == "." || sourceDir == "./" {
					log.Info("[tarball] Skip build/export dir: %s", relPath)
					return filepath.SkipDir
				}
			}

			if shouldExcludeDir(d.Name()) {
				log.Info("[tarball] Skip dir (excluded): %s", relPath)
				return filepath.SkipDir
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}
			h, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return nil
			}
			h.Name = filepath.ToSlash(relPath) + "/"
			dirHeaders = append(dirHeaders, *h)
			return nil
		}
		if outputMode == nextcore.OutputModeDefault {
			if skip, reason := shouldExcludeFile(d.Name(), relPath); skip {
				log.Info("[tarball] Skip file (%s): %s", reason, relPath)
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		jobs = append(jobs, fileJob{
			index:   len(jobs),
			path:    path,
			relPath: filepath.ToSlash(relPath),
			size:    info.Size(),
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk failed: %w", err)
	}

	log.Info("[tarball] Walk done in %s — %d dirs, %d files to archive",
		time.Since(walkStart).Round(time.Millisecond), len(dirHeaders), len(jobs))
	log.Info("[tarball] Phase 2: Writing %d directory entries...", len(dirHeaders))
	for _, h := range dirHeaders {
		hCopy := h
		if err := tw.WriteHeader(&hCopy); err != nil {
			return fmt.Errorf("write dir header %s: %w", h.Name, err)
		}
		log.Info("[tarball]   dir → %s", h.Name)
	}

	log.Info("[tarball] Phase 3: Reading %d files (%d workers)...", len(jobs), workerCount)
	pipelineStart := time.Now()
	fileChan := make(chan fileJob, maxInFlight)
	resultChan := make(chan fileResult, maxInFlight)
	readBufPool := &sync.Pool{
		New: func() interface{} { return make([]byte, 32*1024) },
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for job := range fileChan {
				resultChan <- readFile(job, readBufPool, id, log)
			}
		}(i)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	go func() {
		for _, job := range jobs {
			fileChan <- job
		}
		close(fileChan)
	}()

	h := &resultHeap{}
	heap.Init(h)
	nextExpected := 0
	var processedFiles int64
	lastProgressLog := time.Now()

	for result := range resultChan {
		if result.err != nil {
			return fmt.Errorf("worker error for %s: %w", result.job.relPath, result.err)
		}

		heap.Push(h, result)

		for h.Len() > 0 && (*h)[0].job.index == nextExpected {
			r := heap.Pop(h).(fileResult)

			if err := writeTarEntry(tw, r, log); err != nil {
				return err
			}

			nextExpected++
			atomic.AddInt64(&processedFiles, 1)
			if time.Since(lastProgressLog) > 2*time.Second {
				pct := float64(atomic.LoadInt64(&processedFiles)) / float64(len(jobs)) * 100
				log.Info("[tarball] Progress: %d/%d files (%.1f%%)",
					atomic.LoadInt64(&processedFiles), len(jobs), pct)
				lastProgressLog = time.Now()
			}
		}
	}

	log.Info("[tarball] Pipeline done in %s — %d files written",
		time.Since(pipelineStart).Round(time.Millisecond), processedFiles)

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	if err := bufWriter.Flush(); err != nil {
		return fmt.Errorf("flush buffer: %w", err)
	}
	if err := tarfile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	log.Info("[tarball] Renaming %s → %s", tempName, targetTar)
	// #nosec G703
	if err := os.Rename(tempName, targetTar); err != nil {
		if strings.Contains(err.Error(), "invalid cross-device link") {
			log.Info("[tarball] Cross-device rename detected, falling back to copy...")
			if copyErr := fileCopyAndRemove(tempName, targetTar); copyErr != nil {
				return fmt.Errorf("cross-device copy: %w", copyErr)
			}
		} else {
			return fmt.Errorf("rename: %w", err)
		}
	}

	success = true

	if fi, err := os.Stat(targetTar); err == nil {
		log.Info("[tarball] Done — %d files, %.2f MB", processedFiles, float64(fi.Size())/1024/1024)
	}

	return nil
}

func readFile(job fileJob, pool *sync.Pool, workerID int, log logger) fileResult {
	info, err := os.Lstat(job.path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info("[tarball] worker-%d file vanished: %s", workerID, job.relPath)
			return fileResult{job: job}
		}
		return fileResult{job: job, err: fmt.Errorf("lstat: %w", err)}
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fileResult{job: job, info: info}
	}

	if job.size > largeFileThreshold {
		log.Info("[tarball] worker-%d deferred (large): %s (%.1f MB)",
			workerID, job.relPath, float64(job.size)/1024/1024)
		return fileResult{job: job, info: info, data: nil}
	}

	f, err := os.Open(job.path)
	if err != nil {
		return fileResult{job: job, err: fmt.Errorf("open: %w", err)}
	}
	defer f.Close()
	data := make([]byte, info.Size())
	if _, err := io.ReadFull(f, data); err != nil {
		return fileResult{job: job, err: fmt.Errorf("read: %w", err)}
	}

	log.Info("[tarball] worker-%d read: %s (%d bytes)", workerID, job.relPath, len(data))
	return fileResult{job: job, info: info, data: data}
}

func writeTarEntry(tw *tar.Writer, r fileResult, log logger) error {
	if r.info == nil {
		log.Info("[tarball]   skip (vanished): %s", r.job.relPath)
		return nil
	}

	var linkTarget string
	if r.info.Mode()&os.ModeSymlink != 0 {
		var err error
		linkTarget, err = os.Readlink(r.job.path)
		if err != nil {
			return fmt.Errorf("readlink %s: %w", r.job.path, err)
		}
	}

	header, err := tar.FileInfoHeader(r.info, linkTarget)
	if err != nil {
		return fmt.Errorf("file info header %s: %w", r.job.relPath, err)
	}
	header.Name = r.job.relPath

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write header %s: %w", r.job.relPath, err)
	}

	if !r.info.Mode().IsRegular() {
		return nil
	}

	if r.data != nil {
		if _, err := tw.Write(r.data); err != nil {
			return fmt.Errorf("write buffered %s: %w", r.job.relPath, err)
		}
		log.Info("[tarball]   file → %s (%d bytes, buffered)", header.Name, len(r.data))
		return nil
	}
	f, err := os.Open(r.job.path)
	if err != nil {
		return fmt.Errorf("open large file %s: %w", r.job.path, err)
	}
	defer f.Close()

	written, err := io.CopyBuffer(tw, f, make([]byte, 32*1024))
	if err != nil {
		return fmt.Errorf("stream %s: %w", r.job.path, err)
	}
	log.Info("[tarball]   file → %s (%.1f MB, streamed)", header.Name, float64(written)/1024/1024)
	return nil
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	// #nosec G304
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer source.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	// #nosec G304
	destination, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy %s → %s: %w", src, dst, err)
	}
	return nil
}

func copyDir(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0750)
		}
		return copyFile(path, dstPath)
	})
}

func fileCopyAndRemove(src, dst string) error {
	// #nosec G304, G703
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer source.Close()

	// #nosec G304
	destination, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy %s → %s: %w", src, dst, err)
	}
	// #nosec G703
	_ = os.Remove(src)
	return nil
}
