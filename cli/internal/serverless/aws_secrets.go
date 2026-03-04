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

func (p *AWSProvider) UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error {
	p.log.Info("Securing secrets in AWS Secrets Manager for app: %s...", appName)

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
		// 1. ResourceNotFoundException = secret doesn't exist yet → create it
		var notFound *smTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			p.log.Info("Secret %s does not exist yet, creating...", secretName)
			_, createErr := client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
				Name:         aws.String(secretName),
				SecretString: aws.String(strVal),
			})
			if createErr != nil {
				return fmt.Errorf("failed to create secret in AWS Secrets Manager: %w", createErr)
			}
		} else {
			// 2. InvalidRequestException - might be "marked for deletion"
			if strings.Contains(err.Error(), "marked for deletion") {
				p.log.Info("Secret %s is marked for deletion. Restoring it first...", secretName)
				_, restoreErr := client.RestoreSecret(ctx, &secretsmanager.RestoreSecretInput{
					SecretId: aws.String(secretName),
				})
				if restoreErr != nil {
					return fmt.Errorf("failed to restore secret %s: %w", secretName, restoreErr)
				}
				// Retry update after restoration
				_, retryErr := client.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
					SecretId:     aws.String(secretName),
					SecretString: aws.String(strVal),
				})
				if retryErr != nil {
					return fmt.Errorf("failed to update secret %s after restoration: %w", secretName, retryErr)
				}
			} else {
				// Any other AWS error is a real failure
				return fmt.Errorf("failed to update secret %s: %w", secretName, err)
			}
		}
	}

	p.log.Info("Secrets securely stored: %s", secretName)
	return nil
}
