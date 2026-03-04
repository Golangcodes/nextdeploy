package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/Golangcodes/nextdeploy/shared"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

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

func (p *AWSProvider) DeployStatic(ctx context.Context, tarballPath string, appCfg *cfgTypes.NextDeployConfig, meta *nextcore.NextCorePayload) error {
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

	// We need to unpack the tarball first to access static files
	tmpDir, err := os.MkdirTemp("", "nd-serverless-deploy-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := shared.ExtractTarGz(tarballPath, tmpDir); err != nil {
		return fmt.Errorf("failed to extract tarball: %w", err)
	}

	distDir := meta.DistDir
	if distDir == "" {
		distDir = ".next"
	}

	// Directories to upload to S3
	var uploadDirs []struct {
		Src  string
		Dest string
	}

	if meta.OutputMode == nextcore.OutputModeExport {
		exportDir := meta.ExportDir
		if exportDir == "" {
			exportDir = "out"
		}
		p.log.Info("Detected Export Mode. Syncing %s to bucket root...", exportDir)
		uploadDirs = append(uploadDirs, struct {
			Src  string
			Dest string
		}{Src: filepath.Join(tmpDir, exportDir), Dest: ""})
	} else {
		uploadDirs = []struct {
			Src  string
			Dest string
		}{
			{Src: filepath.Join(tmpDir, "public"), Dest: ""},
			{Src: filepath.Join(tmpDir, distDir, "static"), Dest: "_next/static"},
		}
	}

	for _, dir := range uploadDirs {
		if _, err := os.Stat(dir.Src); os.IsNotExist(err) {
			continue
		}

		err = filepath.Walk(dir.Src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			relPath, err := filepath.Rel(dir.Src, path)
			if err != nil {
				return err
			}

			s3Key := filepath.Join(dir.Dest, relPath)
			// Normalize path for S3
			s3Key = filepath.ToSlash(s3Key)

			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			defer file.Close()

			contentType := detectContentType(path)

			// Add basic Cache-Control
			cacheControl := "public, max-age=31536000, immutable"
			if dir.Dest == "" { // e.g. public directory (favicon, etc) shouldn't be cached forever usually
				cacheControl = "public, max-age=3600"
			}

			_, err = uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
				Bucket:       aws.String(bucketName),
				Key:          aws.String(s3Key),
				Body:         file,
				ContentType:  aws.String(contentType),
				CacheControl: aws.String(cacheControl),
			})

			if err != nil {
				return fmt.Errorf("failed to upload %s to S3: %w", s3Key, err)
			}

			return nil
		})

		if err != nil {
			return fmt.Errorf("failed walking directory %s: %w", dir.Src, err)
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
