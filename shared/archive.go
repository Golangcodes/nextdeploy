package shared

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared/sensitive"
)

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

// CreateTarGz packs srcDir into a gzipped tar at targetTar. Entries are
// relative to srcDir (no leading dir component). Uses system `tar` when
// available so behaviour matches `ExtractTarGz`; no pure-Go fallback yet
// because every supported dev/CI host ships tar.
func CreateTarGz(srcDir, targetTar string) error {
	absTarget, err := filepath.Abs(targetTar)
	if err != nil {
		return err
	}
	absSrc, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	if info, err := os.Stat(absSrc); err != nil {
		return fmt.Errorf("tar source %s: %w", absSrc, err)
	} else if !info.IsDir() {
		return fmt.Errorf("tar source %s is not a directory", absSrc)
	}
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o750); err != nil {
		return fmt.Errorf("mkdir for tar target: %w", err)
	}

	tarPath, err := exec.LookPath("tar")
	if err != nil {
		return fmt.Errorf("tar utility not found in PATH: %w", err)
	}

	// -C srcDir, then pack '.' so entries are stored without a leading
	// directory — symmetric with ExtractTarGz.
	// #nosec G204
	cmd := exec.Command(tarPath, "-czf", absTarget, "-C", absSrc, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar creation failed: %v - %s", err, string(out))
	}
	return nil
}

func CreateZip(srcDir, targetZip string) error {
	absTarget, _ := filepath.Abs(targetZip)
	absSrc, _ := filepath.Abs(srcDir)

	zipPath, err := exec.LookPath("zip")
	if err == nil {
		cmd := exec.Command(zipPath, "-rq9", absTarget, ".")
		cmd.Dir = absSrc
		if out, err := cmd.CombinedOutput(); err != nil {
			sensitive.Printf("System zip failed, falling back: %v - %s\n", err, string(out))
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
