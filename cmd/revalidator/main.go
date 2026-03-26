package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// RevalidateMessage is the payload Next.js normally outputs to the SQS queue
type RevalidateMessage struct {
	Tag  string `json:"tag"`  // e.g. "post-123"
	Path string `json:"path"` // e.g. "/blog/my-post"
}

// TagPathMap matches what's built by nextcore.BuildTagMap
type TagPathMap struct {
	Tags      map[string][]string `json:"tags"`
	Intervals map[string]int      `json:"intervals"`
}

var (
	awsCfg         aws.Config
	tagMap         TagPathMap
	cachedMap      bool
	distributionID string
	bucketName     string
)

func init() {
	var err error
	awsCfg, err = config.LoadDefaultConfig(context.Background())
	if err != nil {
		fmt.Printf("Error loading AWS config: %v\n", err)
	}
	distributionID = os.Getenv("DISTRIBUTION_ID")
	bucketName = os.Getenv("CACHE_BUCKET")
}

func loadTagMap(ctx context.Context) (TagPathMap, error) {
	if cachedMap {
		return tagMap, nil
	}

	client := s3.NewFromConfig(awsCfg)
	fmt.Printf("Loading isr-tag-map.json from bucket %s\n", bucketName)

	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("isr-tag-map.json"),
	})
	if err != nil {
		fmt.Printf("Tag map not found or accessible: %v\n", err)
		return TagPathMap{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return TagPathMap{}, err
	}

	var m TagPathMap
	if err := json.Unmarshal(data, &m); err != nil {
		return TagPathMap{}, err
	}

	tagMap = m
	cachedMap = true
	return tagMap, nil
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	// 1. Load tag map from S3
	tMap, _ := loadTagMap(ctx) // In case of error, we can still fall back to msg.Path

	// 2. Collect unique paths across all SQS messages
	pathsToInvalidate := map[string]struct{}{}
	for _, record := range sqsEvent.Records {
		var msg RevalidateMessage
		if err := json.Unmarshal([]byte(record.Body), &msg); err != nil {
			fmt.Printf("Failed to unmarshal SQS record body: %s\n", record.Body)
			continue
		}

		if msg.Path != "" {
			pathsToInvalidate[msg.Path] = struct{}{}
		}

		// Look up all paths for this tag
		if msg.Tag != "" && tMap.Tags != nil {
			if matchedPaths, ok := tMap.Tags[msg.Tag]; ok {
				for _, p := range matchedPaths {
					pathsToInvalidate[p] = struct{}{}
				}
			}
		}
	}

	if len(pathsToInvalidate) == 0 {
		return nil
	}

	var paths []string
	for p := range pathsToInvalidate {
		paths = append(paths, p)
	}

	fmt.Printf("Invalidating %d paths: %v\n", len(paths), paths)

	// 3. Invalidate only affected paths
	return invalidatePaths(ctx, paths)
}

func invalidatePaths(ctx context.Context, paths []string) error {
	cf := cloudfront.NewFromConfig(awsCfg)

	quantity := int32(len(paths))
	req := &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(distributionID),
		InvalidationBatch: &types.InvalidationBatch{
			CallerReference: aws.String(uuid.New().String()),
			Paths: &types.Paths{
				Quantity: aws.Int32(quantity),
				Items:    paths,
			},
		},
	}

	_, err := cf.CreateInvalidation(ctx, req)
	if err != nil {
		fmt.Printf("Failed to create invalidation: %v\n", err)
		return err
	}

	return nil
}

func main() {
	lambda.Start(handler)
}
