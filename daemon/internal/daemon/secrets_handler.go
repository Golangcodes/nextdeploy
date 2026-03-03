package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

const (
	// #nosec G101
	secretsDir     = "/opt/nextdeploy/secrets"
	errMissingKey  = "missing 'key' argument"
	errLoadSecrets = "failed to load secrets: %v"
)

func (ch *CommandHandler) handleSecrets(args map[string]interface{}) types.Response {
	action, ok := StringArg(args, "action")
	if !ok {
		return types.Response{Success: false, Message: "missing 'action' argument"}
	}

	appName, ok := StringArg(args, "appName")
	if !ok {
		return types.Response{Success: false, Message: "missing 'appName' argument"}
	}
	if err := validateAppName(appName); err != nil {
		return types.Response{Success: false, Message: err.Error()}
	}

	switch action {
	case "set":
		return ch.setSecret(appName, args)
	case "get":
		return ch.getSecret(appName, args)
	case "unset":
		return ch.unsetSecret(appName, args)
	case "list":
		return ch.listSecrets(appName)
	default:
		return types.Response{Success: false, Message: fmt.Sprintf("unknown secrets action: %s", action)}
	}
}

func (ch *CommandHandler) setSecret(appName string, args map[string]interface{}) types.Response {
	key, ok := StringArg(args, "key")
	if !ok {
		return types.Response{Success: false, Message: errMissingKey}
	}
	value, ok := StringArg(args, "value")
	if !ok {
		return types.Response{Success: false, Message: "missing 'value' argument"}
	}

	secrets, err := ch.loadSecrets(appName)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf(errLoadSecrets, err)}
	}

	secrets[key] = value

	if err := ch.saveSecrets(appName, secrets); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to save secrets: %v", err)}
	}

	if err := ch.syncAppSecrets(appName, secrets); err != nil {
		return types.Response{Success: true, Message: fmt.Sprintf("secret set but sync failed: %v", err)}
	}

	return types.Response{Success: true, Message: fmt.Sprintf("secret %s set successfully", key)}
}

func (ch *CommandHandler) getSecret(appName string, args map[string]interface{}) types.Response {
	key, ok := StringArg(args, "key")
	if !ok {
		return types.Response{Success: false, Message: errMissingKey}
	}

	secrets, err := ch.loadSecrets(appName)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf(errLoadSecrets, err)}
	}

	value, exists := secrets[key]
	if !exists {
		return types.Response{Success: false, Message: fmt.Sprintf("secret %s not found", key)}
	}

	return types.Response{Success: true, Message: value}
}

func (ch *CommandHandler) unsetSecret(appName string, args map[string]interface{}) types.Response {
	key, ok := StringArg(args, "key")
	if !ok {
		return types.Response{Success: false, Message: errMissingKey}
	}

	secrets, err := ch.loadSecrets(appName)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf(errLoadSecrets, err)}
	}

	if _, exists := secrets[key]; !exists {
		return types.Response{Success: false, Message: fmt.Sprintf("secret %s not found", key)}
	}

	delete(secrets, key)

	if err := ch.saveSecrets(appName, secrets); err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf("failed to save secrets: %v", err)}
	}

	// Hot-reload application environment
	if err := ch.syncAppSecrets(appName, secrets); err != nil {
		return types.Response{Success: true, Message: fmt.Sprintf("secret unset but sync failed: %v", err)}
	}

	return types.Response{Success: true, Message: fmt.Sprintf("secret %s unset successfully", key)}
}

func (ch *CommandHandler) listSecrets(appName string) types.Response {
	secrets, err := ch.loadSecrets(appName)
	if err != nil {
		return types.Response{Success: false, Message: fmt.Sprintf(errLoadSecrets, err)}
	}

	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return types.Response{
		Success: true,
		Message: strings.Join(keys, "\n"),
	}
}

func (ch *CommandHandler) loadSecrets(appName string) (map[string]string, error) {
	path := filepath.Join(secretsDir, fmt.Sprintf("%s.json", appName))
	// #nosec G304
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	var secrets map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, err
	}

	return secrets, nil
}

func (ch *CommandHandler) saveSecrets(appName string, secrets map[string]string) error {
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return err
	}

	path := filepath.Join(secretsDir, fmt.Sprintf("%s.json", appName))
	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func (ch *CommandHandler) syncAppSecrets(appName string, secrets map[string]string) error {
	appDir := filepath.Join("/opt/nextdeploy/apps", appName, "current")
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		return nil
	}

	envFilePath := filepath.Join(appDir, ".env.nextdeploy")

	var envLines []string
	for k, v := range secrets {
		envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
	}
	content := strings.Join(envLines, "\n") + "\n"
	if err := os.WriteFile(envFilePath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write env file: %w", err)
	}

	log.Printf("[secrets] Updated %s, restarting service...", envFilePath)
	return ch.processManager.RestartService(appName)
}
