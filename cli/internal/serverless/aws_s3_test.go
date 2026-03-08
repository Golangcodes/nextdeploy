package serverless

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/smithy-go"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
)

type fakeUploader struct {
	failuresRemaining *int
	calls             int
}

func (f *fakeUploader) UploadObject(_ context.Context, _ *transfermanager.UploadObjectInput, _ ...func(*transfermanager.Options)) (*transfermanager.UploadObjectOutput, error) {
	f.calls++
	if *f.failuresRemaining > 0 {
		*f.failuresRemaining--
		return nil, &smithy.OperationError{Err: errors.New("temporary s3 upload failure")}
	}
	return &transfermanager.UploadObjectOutput{}, nil
}

func TestUploadAssetWithRetryEventuallySucceeds(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	assetPath := filepath.Join(tmpDir, "asset.txt")
	if err := os.WriteFile(assetPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write temp asset: %v", err)
	}

	remainingFailures := 2
	provider := NewAWSProvider(false)
	uploader := &fakeUploader{failuresRemaining: &remainingFailures}
	asset := packaging.S3Asset{
		LocalPath:    assetPath,
		S3Key:        "static/asset.txt",
		ContentType:  "text/plain",
		CacheControl: "public, max-age=60",
	}

	if err := provider.uploadAssetWithRetry(context.Background(), uploader, "bucket", asset); err != nil {
		t.Fatalf("expected upload to succeed after retries, got error: %v", err)
	}

	if uploader.calls != 3 {
		t.Fatalf("expected 3 upload attempts, got %d", uploader.calls)
	}
}

func TestUploadAssetWithRetryFailsAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	assetPath := filepath.Join(tmpDir, "asset.txt")
	if err := os.WriteFile(assetPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write temp asset: %v", err)
	}

	remainingFailures := s3UploadMaxAttempts
	provider := NewAWSProvider(false)
	uploader := &fakeUploader{failuresRemaining: &remainingFailures}
	asset := packaging.S3Asset{
		LocalPath:    assetPath,
		S3Key:        "static/asset.txt",
		ContentType:  "text/plain",
		CacheControl: "public, max-age=60",
	}

	err := provider.uploadAssetWithRetry(context.Background(), uploader, "bucket", asset)
	if err == nil {
		t.Fatal("expected upload to fail after max attempts")
	}
	if uploader.calls != s3UploadMaxAttempts {
		t.Fatalf("expected %d upload attempts, got %d", s3UploadMaxAttempts, uploader.calls)
	}
}
