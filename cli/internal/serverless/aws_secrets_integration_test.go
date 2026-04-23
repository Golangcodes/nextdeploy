//go:build integration

// Package serverless integration tests.
//
// Run with: mage testIntegration
//
// Requires AWS credentials in the environment with permissions for
// secretsmanager:CreateSecret/UpdateSecret/GetSecretValue/DeleteSecret on
// the test secret name. Tests are namespaced under "nextdeploy/test/<rand>"
// and clean up on success.
package serverless

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/Golangcodes/nextdeploy/shared"
)

func newTestProvider(t *testing.T) *AWSProvider {
	t.Helper()
	if os.Getenv("AWS_REGION") == "" && os.Getenv("AWS_DEFAULT_REGION") == "" {
		t.Skip("AWS_REGION not set; skipping integration test")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	return &AWSProvider{
		log:         shared.PackageLogger("test", "TEST"),
		cfg:         cfg,
		environment: "test",
	}
}

func uniqueAppName() string {
	return fmt.Sprintf("nd-it-%d-%d", time.Now().UnixNano(), rand.Intn(10000))
}

func cleanupSecret(t *testing.T, p *AWSProvider, appName string) {
	t.Helper()
	client := secretsmanager.NewFromConfig(p.cfg)
	_, _ = client.DeleteSecret(context.Background(), &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(p.secretName(appName)),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	})
}

func TestIntegration_SetGetUnset(t *testing.T) {
	p := newTestProvider(t)
	app := uniqueAppName()
	t.Cleanup(func() { cleanupSecret(t, p, app) })

	ctx := context.Background()

	if err := p.SetSecret(ctx, app, "FOO", "bar"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	got, err := p.GetSecrets(ctx, app)
	if err != nil {
		t.Fatalf("GetSecrets: %v", err)
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", got["FOO"])
	}

	if err := p.UnsetSecret(ctx, app, "FOO"); err != nil {
		t.Fatalf("UnsetSecret: %v", err)
	}

	got, _ = p.GetSecrets(ctx, app)
	if _, ok := got["FOO"]; ok {
		t.Errorf("expected FOO to be unset, still present")
	}
}

func TestIntegration_DiffSkipsRedundantWrites(t *testing.T) {
	p := newTestProvider(t)
	app := uniqueAppName()
	t.Cleanup(func() { cleanupSecret(t, p, app) })

	ctx := context.Background()
	want := map[string]string{"A": "1", "B": "2"}

	if err := p.UpdateSecrets(ctx, app, want); err != nil {
		t.Fatalf("first UpdateSecrets: %v", err)
	}

	// Second call with identical content must be a no-op (no new version).
	client := secretsmanager.NewFromConfig(p.cfg)
	before, _ := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(p.secretName(app)),
	})

	if err := p.UpdateSecrets(ctx, app, want); err != nil {
		t.Fatalf("second UpdateSecrets: %v", err)
	}

	after, _ := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(p.secretName(app)),
	})

	if aws.ToString(before.VersionId) != aws.ToString(after.VersionId) {
		t.Errorf("expected no new version on identical write; before=%s after=%s",
			aws.ToString(before.VersionId), aws.ToString(after.VersionId))
	}
}
