package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

func SetupLogger(config types.LoggerConfig) *log.Logger {
	var writer io.Writer = os.Stdout // default: stdout only

	if err := os.MkdirAll(config.LogDir, 0750); err != nil {
		log.Printf("Warning: failed to create log directory %q: %v — logging to stdout only\n", config.LogDir, err)
	} else {
		logFilePath := filepath.Join(config.LogDir, config.LogFileName)
		logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			log.Printf("Warning: failed to open log file %q: %v — logging to stdout only\n", logFilePath, err)
		} else {
			writer = io.MultiWriter(os.Stdout, logFile)
		}
	}

	return log.New(writer, "NEXTDEPLOY: ", log.LstdFlags|log.Lshortfile)
}

func LogInfo(logger *log.Logger, config types.LoggerConfig, message string) {
	logger.Println("INFO:", message)
}

func LogError(logger *log.Logger, config types.LoggerConfig, message string) {
	logger.Println("ERROR:", message)
}
