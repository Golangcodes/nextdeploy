package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smTypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

func (p *AWSProvider) SetSecret(ctx context.Context, appName string, key, value string) error {
	secrets, err := p.GetSecrets(ctx, appName)
	if err != nil {
		return err
	}
	secrets[key] = value
	return p.UpdateSecrets(ctx, appName, secrets)
}

func (p *AWSProvider) UnsetSecret(ctx context.Context, appName string, key string) error {
	secrets, err := p.GetSecrets(ctx, appName)
	if err != nil {
		return err
	}
	if _, ok := secrets[key]; !ok {
		return nil // Already unset
	}
	delete(secrets, key)
	return p.UpdateSecrets(ctx, appName, secrets)
}

func (p *AWSProvider) GetSecrets(ctx context.Context, appName string) (map[string]string, error) {
	client := secretsmanager.NewFromConfig(p.cfg)
	secretName := fmt.Sprintf("nextdeploy/apps/%s/production", appName)

	output, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})

	if err != nil {
		var notFound *smTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("failed to fetch secrets from AWS: %w", err)
	}

	if output.SecretString == nil {
		return make(map[string]string), nil
	}

	var secrets map[string]string
	if err := json.Unmarshal([]byte(*output.SecretString), &secrets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal secrets: %w", err)
	}

	return secrets, nil
}

func (p *AWSProvider) UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error {
	p.log.Info("Syncing secrets to AWS Secrets Manager for app: %s...", appName)

	client := secretsmanager.NewFromConfig(p.cfg)
	secretName := fmt.Sprintf("nextdeploy/apps/%s/production", appName)

	secretString, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}
	strVal := string(secretString)

	// Attempt update first. If the secret doesn't exist yet, create it.
	_, err = client.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(secretName),
		SecretString: aws.String(strVal),
	})

	if err != nil {
		var notFound *smTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			p.log.Info("Secret %s does not exist, creating...", secretName)
			_, createErr := client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
				Name:         aws.String(secretName),
				SecretString: aws.String(strVal),
			})
			if createErr != nil {
				return fmt.Errorf("failed to create secret: %w", createErr)
			}
		} else if strings.Contains(err.Error(), "marked for deletion") {
			p.log.Info("Secret %s is marked for deletion. Restoring...", secretName)
			_, restoreErr := client.RestoreSecret(ctx, &secretsmanager.RestoreSecretInput{
				SecretId: aws.String(secretName),
			})
			if restoreErr != nil {
				return fmt.Errorf("failed to restore secret: %w", restoreErr)
			}
			// Retry update
			_, retryErr := client.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
				SecretId:     aws.String(secretName),
				SecretString: aws.String(strVal),
			})
			if retryErr != nil {
				return fmt.Errorf("failed to update secret after restoration: %w", retryErr)
			}
		} else {
			return fmt.Errorf("failed to update secret %s: %w", secretName, err)
		}
	}

	p.log.Info("Secrets successfully synced to AWS.")
	return nil
}
