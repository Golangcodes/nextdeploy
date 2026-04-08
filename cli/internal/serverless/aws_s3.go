package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

const (
	s3UploadMaxAttempts = 4
	s3UploadBaseDelay   = 500 * time.Millisecond
)

type s3ObjectUploader interface {
	UploadObject(context.Context, *transfermanager.UploadObjectInput, ...func(*transfermanager.Options)) (*transfermanager.UploadObjectOutput, error)
}

func (p *AWSProvider) getS3BucketName(appCfg *cfgTypes.NextDeployConfig) string {
	name := fmt.Sprintf("nextdeploy-%s-%s-assets", appCfg.App.Name, appCfg.App.Environment)
	if p.accountID != "" {
		name = fmt.Sprintf("%s-%s", name, p.accountID)
	}
	return strings.ToLower(name)
}

func (p *AWSProvider) ensureBucketExists(ctx context.Context, client *s3.Client, bucketName, region string) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		return nil // Bucket exists and we have access
	}

	p.log.Info("S3 Bucket %s does not exist, creating in region %s...", bucketName, region)

	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}

	// S3 US-EAST-1 (us-east-1) does not require a LocationConstraint
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3Types.CreateBucketConfiguration{
			LocationConstraint: s3Types.BucketLocationConstraint(region),
		}
	}

	_, err = client.CreateBucket(ctx, createInput)
	if err != nil {
		return fmt.Errorf("failed to create S3 bucket: %w", err)
	}

	p.log.Info("S3 Bucket %s created successfully.", bucketName)
	return nil
}

func (p *AWSProvider) uploadAssetWithRetry(ctx context.Context, uploader s3ObjectUploader, bucketName string, asset packaging.S3Asset) error {
	var lastErr error

	for attempt := 1; attempt <= s3UploadMaxAttempts; attempt++ {
		file, err := os.Open(asset.LocalPath)
		if err != nil {
			return fmt.Errorf("failed to open local asset %s: %w", asset.LocalPath, err)
		}

		_, err = uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
			Bucket:       aws.String(bucketName),
			Key:          aws.String(filepath.ToSlash(asset.S3Key)),
			Body:         file,
			ContentType:  aws.String(asset.ContentType),
			CacheControl: aws.String(asset.CacheControl),
		})
		closeErr := file.Close()
		if err == nil {
			if closeErr != nil {
				return fmt.Errorf("uploaded %s but failed to close local file: %w", asset.S3Key, closeErr)
			}
			return nil
		}
		if closeErr != nil {
			p.log.Warn("Failed to close local asset %s after upload attempt %d: %v", asset.LocalPath, attempt, closeErr)
		}

		lastErr = err
		if attempt == s3UploadMaxAttempts {
			break
		}

		delay := s3UploadBaseDelay * time.Duration(1<<(attempt-1))
		p.log.Warn("Failed to upload %s to S3 (attempt %d/%d): %v. Retrying in %s...", asset.S3Key, attempt, s3UploadMaxAttempts, err, delay)

		select {
		case <-ctx.Done():
			return fmt.Errorf("upload canceled for %s: %w", asset.S3Key, ctx.Err())
		case <-time.After(delay):
		}
	}

	return fmt.Errorf("failed to upload %s after %d attempts: %w", asset.S3Key, s3UploadMaxAttempts, lastErr)
}

func (p *AWSProvider) DeployStatic(ctx context.Context, pkg *packaging.PackageResult, appCfg *cfgTypes.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	bucketName := p.getS3BucketName(appCfg)
	p.log.Info("Syncing static assets to S3 Bucket (%s)...", bucketName)

	if bucketName == "" {
		p.log.Info("S3 Bucket not specified and auto-naming failed, skipping static sync.")
		return nil
	}

	client := s3.NewFromConfig(p.cfg)

	// Ensure bucket exists before uploading
	if err := p.ensureBucketExists(ctx, client, bucketName, appCfg.Serverless.Region); err != nil {
		return fmt.Errorf("failed to ensure S3 bucket exists: %w", err)
	}

	uploader := transfermanager.New(client)

	for _, asset := range pkg.S3Assets {
		if info, statErr := os.Stat(asset.LocalPath); statErr == nil {
			p.verboseLog("  Uploading s3://%s/%s (%s, %s)", bucketName, filepath.ToSlash(asset.S3Key), asset.ContentType, formatBytes(info.Size()))
		}

		if err := p.uploadAssetWithRetry(ctx, uploader, bucketName, asset); err != nil {
			p.log.Warn("%v", err)
		}
	}

	p.log.Info("Static assets successfully synced to S3.")
	return nil
}

// emptyS3Bucket deletes all objects AND all versions/delete-markers so the
// bucket can subsequently be deleted even when versioning is enabled.
func (p *AWSProvider) emptyS3Bucket(ctx context.Context, client *s3.Client, bucketName string) error {
	// Delete all object versions and delete markers
	versionPager := s3.NewListObjectVersionsPaginator(client, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
	})

	for versionPager.HasMorePages() {
		page, err := versionPager.NextPage(ctx)
		if err != nil {
			var noSuchBucket *s3Types.NoSuchBucket
			if errors.As(err, &noSuchBucket) {
				return nil
			}
			return err
		}

		var objects []s3Types.ObjectIdentifier

		for _, v := range page.Versions {
			objects = append(objects, s3Types.ObjectIdentifier{
				Key:       v.Key,
				VersionId: v.VersionId,
			})
		}
		for _, dm := range page.DeleteMarkers {
			objects = append(objects, s3Types.ObjectIdentifier{
				Key:       dm.Key,
				VersionId: dm.VersionId,
			})
		}

		if len(objects) == 0 {
			continue
		}

		_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucketName),
			Delete: &s3Types.Delete{
				Objects: objects,
			},
		})
		if err != nil {
			return err
		}
	}

	// Also sweep non-versioned objects (buckets without versioning)
	listPager := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	})

	for listPager.HasMorePages() {
		page, err := listPager.NextPage(ctx)
		if err != nil {
			var noSuchBucket *s3Types.NoSuchBucket
			if errors.As(err, &noSuchBucket) {
				return nil
			}
			return err
		}

		if len(page.Contents) == 0 {
			continue
		}

		var objects []s3Types.ObjectIdentifier
		for _, obj := range page.Contents {
			objects = append(objects, s3Types.ObjectIdentifier{
				Key: obj.Key,
			})
		}

		_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucketName),
			Delete: &s3Types.Delete{
				Objects: objects,
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// updateS3BucketPolicyForOAC merges the CloudFront OAC statement into the
// existing S3 bucket policy rather than replacing it wholesale.
func (p *AWSProvider) updateS3BucketPolicyForOAC(ctx context.Context, bucketName, distributionId string) error {
	client := s3.NewFromConfig(p.cfg)

	const oacSid = "AllowCloudFrontServicePrincipal"

	newStatement := map[string]interface{}{
		"Sid":    oacSid,
		"Effect": "Allow",
		"Principal": map[string]interface{}{
			"Service": "cloudfront.amazonaws.com",
		},
		"Action": []string{"s3:GetObject", "s3:ListBucket"},
		"Resource": []string{
			fmt.Sprintf("arn:aws:s3:::%s/*", bucketName),
			fmt.Sprintf("arn:aws:s3:::%s", bucketName),
		},
		"Condition": map[string]interface{}{
			"StringEquals": map[string]interface{}{
				"AWS:SourceArn": fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", p.accountID, distributionId),
			},
		},
	}

	// Attempt to read existing policy
	var existingStatements []interface{}
	getPolicyOut, err := client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil && getPolicyOut.Policy != nil {
		var existing map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(*getPolicyOut.Policy), &existing); jsonErr == nil {
			if stmts, ok := existing["Statement"].([]interface{}); ok {
				// Filter out any previous OAC statement so we don't accumulate duplicates
				for _, s := range stmts {
					if sm, ok := s.(map[string]interface{}); ok {
						if sm["Sid"] == oacSid {
							continue
						}
					}
					existingStatements = append(existingStatements, s)
				}
			}
		}
	}

	existingStatements = append(existingStatements, newStatement)

	mergedPolicy := map[string]interface{}{
		"Version":   "2012-10-17",
		"Statement": existingStatements,
	}

	policyJSON, _ := json.Marshal(mergedPolicy)

	_, err = client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucketName),
		Policy: aws.String(string(policyJSON)),
	})
	if err != nil {
		return fmt.Errorf("failed to update S3 bucket policy for OAC: %w", err)
	}

	p.log.Info("S3 Bucket Policy updated to allow CloudFront OAC access.")
	return nil
}

func formatBytes(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func detectContentType(path string) string {
	webMimeTypes := map[string]string{
		".css":   "text/css",
		".js":    "application/javascript",
		".mjs":   "application/javascript",
		".json":  "application/json",
		".html":  "text/html",
		".htm":   "text/html",
		".xml":   "application/xml",
		".svg":   "image/svg+xml",
		".png":   "image/png",
		".jpg":   "image/jpeg",
		".jpeg":  "image/jpeg",
		".gif":   "image/gif",
		".webp":  "image/webp",
		".avif":  "image/avif",
		".ico":   "image/x-icon",
		".woff":  "font/woff",
		".woff2": "font/woff2",
		".ttf":   "font/ttf",
		".otf":   "font/otf",
		".eot":   "application/vnd.ms-fontobject",
		".map":   "application/json",
		".txt":   "text/plain",
		".webm":  "video/webm",
		".mp4":   "video/mp4",
		".pdf":   "application/pdf",
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ct, ok := webMimeTypes[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// DeploymentEntry records a single Lambda deployment with the git commit
// it was built from, so rollbacks can be addressed by commit hash rather than
// opaque S3 keys.
type DeploymentEntry struct {
	S3Key       string `json:"s3_key"`
	GitCommit   string `json:"git_commit,omitempty"`
	GitDirty    bool   `json:"git_dirty,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`
}

// deploymentManifest tracks the history of deployed Lambda zips for a function.
// SchemaVersion 2 uses Entries; SchemaVersion 1 (or absent) uses Deployments
// (legacy []string of S3 keys). The reader normalizes both into Entries.
type deploymentManifest struct {
	SchemaVersion int               `json:"schema_version,omitempty"`
	Entries       []DeploymentEntry `json:"entries,omitempty"`
	// Deployments is the legacy v1 field. It is read for backwards compatibility
	// and dropped on the next write.
	Deployments []string `json:"deployments,omitempty"`
}

// normalize promotes legacy v1 entries into the v2 Entries slice and clears
// the legacy field. After normalization, only Entries should be consulted.
func (m *deploymentManifest) normalize() {
	if len(m.Entries) == 0 && len(m.Deployments) > 0 {
		for _, key := range m.Deployments {
			m.Entries = append(m.Entries, DeploymentEntry{S3Key: key})
		}
	}
	m.Deployments = nil
	m.SchemaVersion = 2
}

// manifestMaxHistory caps deployment retention. Matches the VPS daemon's
// pruneReleases keep value (5) so rollback semantics are uniform across targets.
const manifestMaxHistory = 5

// selectRollbackTarget picks a deployment from `history` (oldest-first) based on
// the rollback options. ToCommit takes precedence over Steps; ToCommit matches
// any entry whose GitCommit has the given hex prefix. With Steps=N (default 1),
// the target is the entry N positions before the most recent one. The current
// (last) entry is never returned, since rolling back to the active deployment
// is a no-op.
func selectRollbackTarget(history []DeploymentEntry, opts RollbackOptions) (DeploymentEntry, error) {
	if len(history) == 0 {
		return DeploymentEntry{}, fmt.Errorf("no deployment history available; ship your app first")
	}

	if opts.ToCommit != "" {
		needle := strings.ToLower(strings.TrimSpace(opts.ToCommit))
		// Walk newest-first so a prefix collision picks the most recent build of that commit.
		for i := len(history) - 1; i >= 0; i-- {
			if i == len(history)-1 {
				continue // skip the active deployment
			}
			if strings.HasPrefix(strings.ToLower(history[i].GitCommit), needle) {
				return history[i], nil
			}
		}
		return DeploymentEntry{}, fmt.Errorf("commit %q not found in deployment history (retention=%d). Older deployments may have been pruned", opts.ToCommit, manifestMaxHistory)
	}

	steps := opts.Steps
	if steps <= 0 {
		steps = 1
	}
	if len(history) < steps+1 {
		return DeploymentEntry{}, fmt.Errorf("not enough deployment history to rollback %d step(s) (found %d, need at least %d). Ship your app more times or use a smaller --steps", steps, len(history), steps+1)
	}
	return history[len(history)-1-steps], nil
}

// shortCommit returns a 7-char prefix of a git commit hash, or "nogit" when
// the commit is unavailable. Used for human-readable S3 zip keys.
func shortCommit(full string) string {
	if len(full) >= 7 {
		return full[:7]
	}
	if full == "" {
		return "nogit"
	}
	return full
}

// saveLambdaZipToS3 uploads the lambda zip under a versioned key and appends a
// DeploymentEntry to the history manifest. The S3 key embeds the short git
// commit so deployments are recognizable in the bucket listing. Returns the
// S3 key of the uploaded zip.
func (p *AWSProvider) saveLambdaZipToS3(ctx context.Context, bucketName, functionName string, zipContents []byte, meta *nextcore.NextCorePayload) (string, error) {
	client := s3.NewFromConfig(p.cfg)

	timestamp := time.Now().UTC().Format("20060102T150405Z")
	var commit, generatedAt string
	var dirty bool
	if meta != nil {
		commit = meta.GitCommit
		dirty = meta.GitDirty
		generatedAt = meta.GeneratedAt
	}
	zipKey := fmt.Sprintf("deployments/%s/%s-%s.zip", functionName, timestamp, shortCommit(commit))

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucketName),
		Key:           aws.String(zipKey),
		Body:          bytes.NewReader(zipContents),
		ContentLength: aws.Int64(int64(len(zipContents))),
		ContentType:   aws.String("application/zip"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload lambda zip to S3: %w", err)
	}

	// Update manifest
	manifestKey := fmt.Sprintf("deployments/%s/history.json", functionName)
	manifest := &deploymentManifest{}

	// Try to read existing manifest
	getOut, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(manifestKey),
	})
	if err == nil {
		if jsonErr := json.NewDecoder(getOut.Body).Decode(manifest); jsonErr != nil {
			manifest = &deploymentManifest{} // reset on parse error
		}
		getOut.Body.Close()
	}
	manifest.normalize()

	manifest.Entries = append(manifest.Entries, DeploymentEntry{
		S3Key:       zipKey,
		GitCommit:   commit,
		GitDirty:    dirty,
		GeneratedAt: generatedAt,
	})
	// Trim to max history
	if len(manifest.Entries) > manifestMaxHistory {
		manifest.Entries = manifest.Entries[len(manifest.Entries)-manifestMaxHistory:]
	}

	manifestBytes, _ := json.Marshal(manifest)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(manifestKey),
		Body:        bytes.NewReader(manifestBytes),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return zipKey, fmt.Errorf("failed to update deployment manifest: %w", err)
	}

	return zipKey, nil
}

// getLambdaDeploymentHistory returns the deployment entries from the manifest,
// oldest-first. Legacy v1 manifests (bare []string) are normalized into entries
// with empty git metadata so callers always see a uniform shape.
func (p *AWSProvider) getLambdaDeploymentHistory(ctx context.Context, bucketName, functionName string) ([]DeploymentEntry, error) {
	client := s3.NewFromConfig(p.cfg)
	manifestKey := fmt.Sprintf("deployments/%s/history.json", functionName)

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(manifestKey),
	})
	if err != nil {
		return nil, fmt.Errorf("no deployment history found (run nextdeploy ship at least twice first): %w", err)
	}
	defer out.Body.Close()

	var manifest deploymentManifest
	if err := json.NewDecoder(out.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to parse deployment manifest: %w", err)
	}
	manifest.normalize()
	return manifest.Entries, nil
}
