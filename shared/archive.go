package shared

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ExtractTarGz extracts a gzipped tarball using the system's `tar` utility for high performance.
func ExtractTarGz(src, dest string) error {
	if err := os.MkdirAll(dest, 0750); err != nil {
		return fmt.Errorf("mkdir %s: %w", dest, err)
	}

	tarPath, err := exec.LookPath("tar")
	if err != nil {
		return fmt.Errorf("tar utility not found in PATH: %w", err)
	}

	// #nosec G204
	cmd := exec.Command(tarPath, "--no-same-owner", "--no-same-permissions", "-xzf", src, "-C", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar extraction failed: %v - %s", err, string(out))
	}
	return nil
}

// CreateZip zips the contents of the source directory into the target zip file.
func CreateZip(srcDir, targetZip string) error {
	absTarget, _ := filepath.Abs(targetZip)
	absSrc, _ := filepath.Abs(srcDir)

	zipPath, err := exec.LookPath("zip")
	if err == nil {
		// Use system zip for performance
		// -r = recursive, -j = junk paths (don't record directory names) - but wait, we WANT directory names for Next.js
		// -q = quiet, -9 = best compression
		// We CD into srcDir to zip everything FROM there.
		cmd := exec.Command(zipPath, "-rq9", absTarget, ".")
		cmd.Dir = absSrc
		if out, err := cmd.CombinedOutput(); err != nil {
			// Fallback to Go implementation if system zip fails
			fmt.Printf("System zip failed, falling back: %v - %s\n", err, string(out))
		} else {
			return nil
		}
	}

	zipFile, err := os.Create(targetZip)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		absPath, _ := filepath.Abs(path)
		if absPath == absTarget {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)

		if d.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}
