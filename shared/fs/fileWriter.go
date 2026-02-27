package fs

import (
	"fmt"
	"os"
)

type FileWriter struct {
	force  bool
	dryRun bool
}

func NewFileWriter(force, dryRun bool) *FileWriter {
	return &FileWriter{force: force, dryRun: dryRun}
}

func (fw *FileWriter) Write(path string, content []byte) error {
	if _, err := os.Stat(path); err == nil && !fw.force {
		fmt.Printf("! Skipped: %s already exists\n", path)
		return nil
	}

	if fw.dryRun {
		fmt.Printf("✓ Would create: %s\n", path)
		return nil
	}

	err := os.WriteFile(path, content, 0600)
	if err != nil {
		return err
	}

	fmt.Printf("✓ Created: %s\n", path)
	return nil
}
