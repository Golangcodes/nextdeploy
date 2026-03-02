package shared

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

func EncodeToBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func DecodeFromBase64(encoded string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}
	return data, nil
}
func EncodeToHex(data []byte) string {
	return hex.EncodeToString(data)
}
func DecodeFromHex(encoded string) ([]byte, error) {
	data, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}
	return data, nil
}

func HasRequiredRole(role Identity, Role string) bool {
	return false
}

func SerializeToJSON(data interface{}) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to serialize to JSON: %w", err)
	}
	return string(jsonData), nil
}
func DeserializeFromJSON(jsonStr string, data interface{}) error {
	err := json.Unmarshal([]byte(jsonStr), data)
	if err != nil {
		return fmt.Errorf("failed to deserialize from JSON: %w", err)
	}
	return nil
}

// validate key id
func ValidateKeyID(keyID string) error {
	if len(keyID) != 64 {
		return errors.New("invalid key ID length, must be 64 characters")
	}
	if _, err := hex.DecodeString(keyID); err != nil {
		return fmt.Errorf("invalid key ID format: %w", err)
	}
	return nil
}
