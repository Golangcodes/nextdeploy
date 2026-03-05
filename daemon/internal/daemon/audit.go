package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

type AuditEntry struct {
	Timestamp      time.Time   `json:"timestamp"`
	CommandType    string      `json:"command_type"`
	ClientIdentity string      `json:"client_identity"`
	Result         string      `json:"result"`
	ErrorDetails   string      `json:"error_details,omitempty"`
	Args           interface{} `json:"args,omitempty"`
}

type AuditLogger struct {
	path string
}

func NewAuditLogger(path string) *AuditLogger {
	// Ensure log directory exists
	_ = os.MkdirAll(filepath.Dir(path), 0750)
	return &AuditLogger{path: path}
}

func (al *AuditLogger) Log(entry AuditEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[audit] Error marshaling audit entry: %v", err)
		return
	}

	// Use O_APPEND to keep a history
	// #nosec G304
	f, err := os.OpenFile(al.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("[audit] Error opening audit log: %v", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Printf("[audit] Error writing audit log: %v", err)
	}
}
