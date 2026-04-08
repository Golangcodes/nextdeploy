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

// SetSecret performs an optimistic read-modify-write that survives concurrent
// writers. See mutateSecrets for the concurrency model.
func (p *AWSProvider) SetSecret(ctx context.Context, appName string, key, value string) error {
	return p.mutateSecrets(ctx, appName, func(s map[string]string) (bool, error) {
		s[key] = value
		return true, nil
	})
}

// UnsetSecret performs an optimistic read-modify-write that survives concurrent
// writers. Returns nil if the key was already absent.
func (p *AWSProvider) UnsetSecret(ctx context.Context, appName string, key string) error {
	return p.mutateSecrets(ctx, appName, func(s map[string]string) (bool, error) {
		if _, ok := s[key]; !ok {
			return false, nil // no-op
		}
		delete(s, key)
		return true, nil
	})
}

// mutateSecrets implements optimistic concurrency for read-modify-write
// operations against AWS Secrets Manager.
//
// AWS Secrets Manager has no native conditional-write API on SecretString, so
// we approximate compare-and-swap with this loop:
//
//  1. GetSecretValue → record VersionId v1
//  2. Apply caller's mutation
//  3. GetSecretValue again → if VersionId == v1, write. If a different version
//     appeared, another writer raced us → retry from (1).
//
// A small race window remains between step 3 and the PUT, but it is orders of
// magnitude smaller than the original read-then-write. Bounded retries
// prevent live-lock under heavy contention.
//
// The mutator returns (changed bool, err) so a no-op (e.g. unsetting a key
// that does not exist) skips the write entirely.
func (p *AWSProvider) mutateSecrets(ctx context.Context, appName string, mutate func(map[string]string) (bool, error)) error {
	const maxAttempts = 5
	client := secretsmanager.NewFromConfig(p.cfg)
	secretName := p.secretName(appName)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		current, versionId, err := p.fetchSecretsWithVersion(ctx, client, secretName)
		if err != nil {
			return err
		}

		changed, mErr := mutate(current)
		if mErr != nil {
			return mErr
		}
		if !changed {
			return nil
		}

		// Re-check version right before write. If another writer committed
		// between our initial fetch and now, retry the whole mutation.
		_, latestVersion, err := p.fetchSecretsWithVersion(ctx, client, secretName)
		if err != nil {
			return err
		}
		if latestVersion != versionId {
			p.log.Warn("Concurrent write detected on %s (version %s → %s), retrying (%d/%d)...",
				secretName, versionId, latestVersion, attempt, maxAttempts)
			continue
		}

		if err := p.UpdateSecrets(ctx, appName, current); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("mutateSecrets: exceeded %d attempts due to concurrent writers on %s", maxAttempts, secretName)
}

// fetchSecretsWithVersion fetches the current secret blob plus the AWSCURRENT
// VersionId. Missing secrets return an empty map and "" version (no error).
func (p *AWSProvider) fetchSecretsWithVersion(ctx context.Context, client *secretsmanager.Client, secretName string) (map[string]string, string, error) {
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})
	if err != nil {
		var notFound *smTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return map[string]string{}, "", nil
		}
		return nil, "", fmt.Errorf("fetch secret %s: %w", secretName, err)
	}

	versionId := aws.ToString(out.VersionId)
	if out.SecretString == nil {
		return map[string]string{}, versionId, nil
	}

	var secrets map[string]string
	if err := json.Unmarshal([]byte(*out.SecretString), &secrets); err != nil {
		return nil, "", fmt.Errorf("unmarshal secret %s: %w", secretName, err)
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	return secrets, versionId, nil
}

func (p *AWSProvider) GetSecrets(ctx context.Context, appName string) (map[string]string, error) {
	client := secretsmanager.NewFromConfig(p.cfg)
	secretName := p.secretName(appName)

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
	secretName := p.secretName(appName)

	secretString, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}
	strVal := string(secretString)

	// Skip the write entirely if the remote already matches. Avoids polluting
	// version history and saves API calls. Compares semantically (map equality)
	// rather than byte-for-byte so JSON key ordering doesn't cause false diffs.
	if existing, getErr := p.GetSecrets(ctx, appName); getErr == nil && secretsEqual(existing, secrets) {
		p.log.Info("Secrets unchanged, skipping write to AWS.")
		return nil
	}

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

// secretsEqual reports whether two flat secret maps are semantically equal.
func secretsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
