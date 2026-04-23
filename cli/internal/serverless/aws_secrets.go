package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smTypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/Golangcodes/nextdeploy/shared"
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

		// mutateSecrets already holds the pre-image (pre-mutation `current`) and
		// has verified via CAS that the remote hasn't moved, so the diff-before-
		// write inside public UpdateSecrets would be pure waste here. Skip it by
		// calling writeSecretBlob directly.
		if err := p.writeSecretBlob(ctx, client, secretName, current); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("mutateSecrets: exceeded %d attempts due to concurrent writers on %s", maxAttempts, secretName)
}

// fetchSecretBlob fetches the current secret map plus the AWSCURRENT VersionId.
// Missing secrets return an empty map and "" version (no error). Used by both
// the public GetSecrets path and the CAS loop in mutateSecrets.
func (p *AWSProvider) fetchSecretBlob(ctx context.Context, client *secretsmanager.Client, secretName string) (map[string]string, string, error) {
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

func (p *AWSProvider) fetchSecretsWithVersion(ctx context.Context, client *secretsmanager.Client, secretName string) (map[string]string, string, error) {
	return p.fetchSecretBlob(ctx, client, secretName)
}

func (p *AWSProvider) GetSecrets(ctx context.Context, appName string) (map[string]string, error) {
	client := secretsmanager.NewFromConfig(p.cfg)
	secrets, _, err := p.fetchSecretBlob(ctx, client, p.secretName(appName))
	return secrets, err
}

// UpdateSecrets is the public write entry point. It diffs against the remote
// blob first to avoid polluting version history and save API calls, then
// delegates to writeSecretBlob. Callers that already hold the pre-image
// (e.g. mutateSecrets) should call writeSecretBlob directly to skip the diff.
func (p *AWSProvider) UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error {
	p.log.Info("Syncing secrets to AWS Secrets Manager for app: %s...", appName)

	client := secretsmanager.NewFromConfig(p.cfg)
	secretName := p.secretName(appName)

	// Skip the write entirely if the remote already matches. Compares semantically
	// (map equality) rather than byte-for-byte so JSON key ordering doesn't cause
	// false diffs.
	if existing, getErr := p.GetSecrets(ctx, appName); getErr == nil && secretsEqual(existing, secrets) {
		p.log.Info("Secrets unchanged, skipping write to AWS.")
		return nil
	}

	return p.writeSecretBlob(ctx, client, secretName, secrets)
}

// writeSecretBlob marshals and writes the given map to Secrets Manager at
// secretName. Creates the secret if it doesn't exist, restores + retries if
// marked for deletion. No diffing — callers that need it should use
// UpdateSecrets.
func (p *AWSProvider) writeSecretBlob(ctx context.Context, client *secretsmanager.Client, secretName string, secrets map[string]string) error {
	secretString, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}
	strVal := string(secretString)

	updateIn := &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(secretName),
		SecretString: aws.String(strVal),
	}
	// KmsKeyId on UpdateSecret rotates the key on existing secrets; empty
	// leaves the prior key in place (AWS treats omit as "no change").
	if p.secretsKmsKeyId != "" {
		updateIn.KmsKeyId = aws.String(p.secretsKmsKeyId)
	}

	_, err = client.UpdateSecret(ctx, updateIn)
	if err == nil {
		p.refreshAuditTags(ctx, client, secretName)
		p.log.Info("Secrets successfully synced to AWS.")
		return nil
	}

	var notFound *smTypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		p.log.Info("Secret %s does not exist, creating...", secretName)
		createIn := &secretsmanager.CreateSecretInput{
			Name:         aws.String(secretName),
			SecretString: aws.String(strVal),
			Tags:         p.auditTags(),
		}
		if p.secretsKmsKeyId != "" {
			createIn.KmsKeyId = aws.String(p.secretsKmsKeyId)
		}
		if _, createErr := client.CreateSecret(ctx, createIn); createErr != nil {
			return fmt.Errorf("failed to create secret: %w", createErr)
		}
		p.log.Info("Secrets successfully synced to AWS.")
		return nil
	}

	if isSecretMarkedForDeletion(err) {
		p.log.Info("Secret %s is marked for deletion. Restoring...", secretName)
		if _, restoreErr := client.RestoreSecret(ctx, &secretsmanager.RestoreSecretInput{
			SecretId: aws.String(secretName),
		}); restoreErr != nil {
			return fmt.Errorf("failed to restore secret: %w", restoreErr)
		}
		if _, retryErr := client.UpdateSecret(ctx, updateIn); retryErr != nil {
			return fmt.Errorf("failed to update secret after restoration: %w", retryErr)
		}
		p.refreshAuditTags(ctx, client, secretName)
		p.log.Info("Secrets successfully synced to AWS.")
		return nil
	}

	return fmt.Errorf("failed to update secret %s: %w", secretName, err)
}

// auditTags returns the LastDeployed* tags applied to every secret write.
// Versions in Secrets Manager are immutable and cannot be tagged individually,
// so these reflect only the latest write. Historical audit data lives in
// CloudTrail (TagResource + PutSecretValue events).
func (p *AWSProvider) auditTags() []smTypes.Tag {
	return []smTypes.Tag{
		{Key: aws.String("LastDeployedBy"), Value: aws.String(sanitizeTagValue(p.callerArn))},
		{Key: aws.String("LastGitCommit"), Value: aws.String(sanitizeTagValue(shared.Commit))},
		{Key: aws.String("LastDeployedAt"), Value: aws.String(time.Now().UTC().Format(time.RFC3339))},
		{Key: aws.String("ManagedBy"), Value: aws.String("nextdeploy")},
	}
}

// refreshAuditTags overwrites the LastDeployed* tags after an UpdateSecret.
// Best-effort: a missing secretsmanager:TagResource permission logs a warning
// but never fails the secret write.
func (p *AWSProvider) refreshAuditTags(ctx context.Context, client *secretsmanager.Client, secretName string) {
	_, err := client.TagResource(ctx, &secretsmanager.TagResourceInput{
		SecretId: aws.String(secretName),
		Tags:     p.auditTags(),
	})
	if err != nil {
		p.log.Warn("Audit tagging of %s failed (non-fatal, grant secretsmanager:TagResource to fix): %v", secretName, err)
	}
}

// sanitizeTagValue trims a tag value to the 256-char Secrets Manager limit
// and falls back to "unknown" on empty. Empty Value is rejected by the API.
func sanitizeTagValue(v string) string {
	if v == "" {
		return "unknown"
	}
	if len(v) > 256 {
		return v[:256]
	}
	return v
}

// isSecretMarkedForDeletion reports whether err signals that the target secret
// is pending deletion and can be restored. Secrets Manager returns
// InvalidRequestException for several unrelated conditions (versioning,
// KMS issues, deletion state), so we intersect the typed check with the
// message substring — defense in depth if AWS reformats it.
func isSecretMarkedForDeletion(err error) bool {
	if err == nil {
		return false
	}
	var invalidReq *smTypes.InvalidRequestException
	if !errors.As(err, &invalidReq) {
		return false
	}
	return strings.Contains(err.Error(), "marked for deletion")
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
