package serverless

import (
	"context"
	"fmt"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

func Deploy(ctx context.Context, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	log := shared.PackageLogger("serverless", "☁️  SERVERLESS")
	log.Info("Serverless deployment is coming soon")
	log.Info("Initiating Serverless Deployment to %s...", cfg.Serverless.Provider)

	if cfg.Serverless.Provider != "aws" {
		return fmt.Errorf("unsupported serverless provider: %s", cfg.Serverless.Provider)
	}

	log.Info("Translating RoutePlan to AWS CloudFront Cache Behaviors...")

	log.Info("Syncing static assets to S3 Bucket (%s)...", cfg.Serverless.S3Bucket)

	log.Info("Packaging Node.js standalone server with AWS Lambda Web Adapter...")

	log.Info("Deploying Lambda Function and updating CloudFront Distribution (%s)...", cfg.Serverless.CloudFrontId)

	log.Info("Serverless Deployment architecture scaffolded successfully!")
	return nil
}
