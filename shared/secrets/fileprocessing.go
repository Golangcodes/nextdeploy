package secrets

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (sm *SecretManager) EncryptEnvFile(masterKey string) (map[string]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(cwd, "*.env"))
	if err != nil {
		return nil, fmt.Errorf("failed to find .env files: %w", err)
	}

	if len(files) == 0 {
		SLogs.Warn("No .env files found in the current directory")
		return nil, nil
	}

	results := make(map[string]string)
	for _, file := range files {
		SLogs.Info("Encrypting .env file: %s", file)

		encryptedFile := file + ".enc"
		err := sm.encryptWithOpenSSL(file, encryptedFile, masterKey)
		if err != nil {
			SLogs.Error("Failed to encrypt file %s: %v", file, err)
			return nil, fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
		}

		results[file] = encryptedFile
	}

	return results, nil
}

func (sm *SecretManager) EncryptFile(filename string, key []byte) error {
	encryptedFilename := filename + ".enc"
	return sm.encryptWithOpenSSL(filename, encryptedFilename, string(key))
}

func (sm *SecretManager) encryptWithOpenSSL(inputPath, outputPath, password string) error {
	cmd := exec.Command("openssl", "enc", "-aes-256-cbc", "-salt",
		"-in", inputPath, "-out", outputPath, "-pass", "pass:"+password, "-pbkdf2")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("openssl encryption failed: %v, stderr: %s", err, stderr.String())
	}

	if err := os.Chmod(outputPath, 0600); err != nil {
		return fmt.Errorf("failed to set permissions on encrypted file: %w", err)
	}

	SLogs.Info("Successfully encrypted file %s to %s", inputPath, outputPath)
	return nil
}

func (sm *SecretManager) DecryptFile(filename string, key []byte) (string, error) {
	if !strings.HasSuffix(filename, ".enc") {
		return "", fmt.Errorf("file %s is not an encrypted file", filename)
	}

	decryptedFilename := strings.TrimSuffix(filename, ".enc")

	command, err := sm.decryptWithOpenSSL(filename, decryptedFilename, string(key))
	if err != nil {
		return "", fmt.Errorf("failed to decrypt file %s: %w", filename, err)
	}
	SLogs.Info("Decrypted file %s to %s using command: %s", filename, decryptedFilename, command)

	return command, nil
}

func (sm *SecretManager) decryptWithOpenSSL(inputPath, outputPath, password string) (string, error) {
	// decrypt command is
	command := "openssl enc -d -aes-256-cbc -in .env.enc -out .env -pass pass:%password"
	return command, nil
}
