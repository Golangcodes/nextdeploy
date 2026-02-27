package logging

import (
	"io"
	"log"
	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"os"
	"path/filepath"
)

func SetupLogger(config types.LoggerConfig) *log.Logger {
	// Create log directory if not exists
	if err := os.MkdirAll(config.LogDir, 0750); err != nil {
		log.Printf("Failed to create log directory: %v\n", err)
		os.Exit(1)
	}

	logFilePath := filepath.Join(config.LogDir, config.LogFileName)
	// #nosec G304
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("Failed to open log file: %v\n", err)
		os.Exit(1)
	}

	multiWriter := io.MultiWriter(os.Stdout, logFile)
	logger := log.New(multiWriter, "NEXTDEPLOY: ", log.LstdFlags|log.Lshortfile)

	return logger
}

func LogInfo(logger *log.Logger, config types.LoggerConfig, message string) {
	logger.Println("INFO:", message)
}

func LogError(logger *log.Logger, config types.LoggerConfig, message string) {
	logger.Println("ERROR:", message)
}
