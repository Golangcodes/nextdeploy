package shared

import (
	"crypto/ecdh"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type AgentType string

const (
	AgentDaemon    AgentType = "daemon"
	AgentCLI       AgentType = "cli"
	AgentDashboard AgentType = "dashboard"
)

type MessageType string

const (
	TypeCommand         MessageType = "command"
	TypeCommandResponse MessageType = "command_response"
	TypeStatus          MessageType = "status"
	TypeResponse        MessageType = "response"
	TypeEvent           MessageType = "event"
	TypeLog             MessageType = "log"
	TypeError           MessageType = "error"
	TypeAuth            MessageType = "auth"
	TypeStatusAck       MessageType = "status_ack"
	TypeAuthResponse    MessageType = "auth_response"
)

type AgentMessage struct {
	Source    AgentType         `json:"source"`
	Target    AgentType         `json:"target"`
	Type      MessageType       `json:"type"`
	Payload   json.RawMessage   `json:"payload"`
	Timestamp int64             `json:"timestamp"`
	AgentID   string            `json:"agent_id"`
	Signature string            `json:"signature,omitempty"`
	Context   map[string]string `json:"context,omitempty"`
}

type CommandPayload struct {
	Name string      `json:"name"`
	Args []string    `json:"args,omitempty"`
	ID   string      `json:"id"`
	Meta interface{} `json:"meta,omitempty"`
}

type StatusPayload struct {
	Status  string                 `json:"status"`
	Metrics map[string]interface{} `json:"metrics,omitempty"`
	Load    SystemLoad             `json:"load,omitempty"`
}

type SystemLoad struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
	Disk   float64 `json:"disk"`
}

type AuthPayload struct {
	Token    string `json:"token"`
	Version  string `json:"version"`
	Hostname string `json:"hostname,omitempty"`
}

type EventPayload struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type ErrorPayload struct {
	Message string `json:"message"`
	Code    int    `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

func GetCurrentTimestamp() int64 {
	return time.Now().Unix()
}

func NewCommandMessage(agentID string, command CommandPayload) (AgentMessage, error) {
	payload, err := json.Marshal(command)
	if err != nil {
		return AgentMessage{}, err
	}

	return AgentMessage{
		Type:      TypeCommand,
		Timestamp: GetCurrentTimestamp(),
		AgentID:   agentID,
		Payload:   payload,
	}, nil
}

func NewStatusMessage(agentID string, status StatusPayload) (AgentMessage, error) {
	payload, err := json.Marshal(status)
	if err != nil {
		return AgentMessage{}, err
	}

	return AgentMessage{
		Type:      TypeStatus,
		Timestamp: GetCurrentTimestamp(),
		AgentID:   agentID,
		Payload:   payload,
	}, nil
}

type EncryptedEnv struct {
	KeyID        string            `json:"key_id"`
	EnvBlob      string            `json:"env_blob"`
	Variables    map[string]string `json:"variables"`
	Nonce        string            `json:"nonce"`
	Timestamp    time.Time         `json:"timestamp"`
	CLIPublicKey string            `json:"cli_public_key"`
}

const (
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleDeployer = "deployer"
	RoleReader   = "reader"
)

type Identity struct {
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	SignPublic  string    `json:"sign_public"`
	Role        string    `json:"role"`
	Email       string    `json:"email"`
	AddedBy     string    `json:"added_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type Envelope struct {
	Payload   []byte `json:"payload"`
	Signature string `json:"signature"`
}

type PublicKeyResponse struct {
	KeyID      string `json:"key_id"`
	PublicKey  string `json:"public_key"`
	SignPublic string `json:"sign_public"`
}

type TrustedKey struct {
	KeyID       string          `json:"key_id"`
	PublicKey   *ecdh.PublicKey `json:"public_key"`
	SignPublic  string          `json:"sign_public"`
	Fingerprint string          `json:"fingerprint"`
}

type TrustStore struct {
	Keys       []TrustedKey `json:"keys"`
	Identities []Identity   `json:"identities"`
}

type AuditLogEntry struct {
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	Target    string    `json:"target"`
	Timestamp time.Time `json:"timestamp"`
	Signature string    `json:"signature"`
	IP        string    `json:"ip,omitempty"`
	Message   string    `json:"message"`
	Client    string    `json:"client_id"`
}

type EnvFile struct {
	Variables map[string]string
	Raw       []byte
}

func ParseEnvFile(content []byte) (*EnvFile, error) {
	variables := make(map[string]string)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			SharedLogger.Error("Invalid line in .env file: %s", line)
			return nil, fmt.Errorf("invalid line in .env file: %s", line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			SharedLogger.Error("Empty key or value in .env file: %s", line)
			return nil, fmt.Errorf("empty key or value in .env file: %s", line)
		}
		variables[key] = value
	}
	return &EnvFile{
		Variables: variables,
		Raw:       content,
	}, nil
}
