package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Reserved concurrency for the ISR revalidation Lambda. Capped to prevent a
// revalidation burst from starving the main request Lambda for account-wide
// concurrency. See C10 in REVIEW.md.
const revalReservedConcurrency int32 = 5

// Maximum redeliveries before SQS pushes a message to the DLQ. Three is the
// AWS-recommended floor for FIFO queues.
const sqsMaxReceiveCount = 3

// ensureRevalidationQueueExists creates (idempotently) the main FIFO queue
// for ISR revalidation messages plus a sibling DLQ. Returns the main queue's
// URL and ARN. The DLQ is wired via RedrivePolicy so poison messages don't
// loop forever (see C9 in REVIEW.md).
func (p *AWSProvider) ensureRevalidationQueueExists(ctx context.Context, appName string) (string, string, error) {
	client := sqs.NewFromConfig(p.cfg)

	// 1. DLQ first — we need its ARN to set the RedrivePolicy on the main queue.
	dlqName := fmt.Sprintf("nextdeploy-%s-revalidation-dlq.fifo", appName)
	dlqUrl, dlqArn, err := p.ensureFifoQueue(ctx, client, dlqName, nil)
	if err != nil {
		return "", "", fmt.Errorf("ensure DLQ %s: %w", dlqName, err)
	}
	p.verboseLog("  Revalidation DLQ ready: %s", dlqUrl)

	// 2. Main queue with RedrivePolicy pointing at the DLQ.
	queueName := fmt.Sprintf("nextdeploy-%s-revalidation.fifo", appName)
	redrive, _ := json.Marshal(map[string]any{
		"deadLetterTargetArn": dlqArn,
		"maxReceiveCount":     sqsMaxReceiveCount,
	})
	mainAttrs := map[string]string{
		"RedrivePolicy": string(redrive),
	}
	queueUrl, queueArn, err := p.ensureFifoQueue(ctx, client, queueName, mainAttrs)
	if err != nil {
		return "", "", fmt.Errorf("ensure main queue %s: %w", queueName, err)
	}
	p.log.Info("Revalidation queue ready: %s (DLQ: %s)", queueName, dlqName)
	return queueUrl, queueArn, nil
}

// ensureFifoQueue creates a FIFO queue with content-based dedup and applies
// any extra attributes. If the queue already exists, looks it up by name and
// patches attributes via SetQueueAttributes (CreateQueue is idempotent only
// when attributes match exactly).
func (p *AWSProvider) ensureFifoQueue(
	ctx context.Context,
	client *sqs.Client,
	queueName string,
	extraAttrs map[string]string,
) (string, string, error) {
	attrs := map[string]string{
		"FifoQueue":                 "true",
		"ContentBasedDeduplication": "true",
	}
	for k, v := range extraAttrs {
		attrs[k] = v
	}

	createOut, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(queueName),
		Attributes: attrs,
	})

	var queueUrl string
	switch {
	case err == nil:
		queueUrl = aws.ToString(createOut.QueueUrl)
	case isSqsAlreadyExistsErr(err):
		getOut, getErr := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
			QueueName: aws.String(queueName),
		})
		if getErr != nil {
			return "", "", fmt.Errorf("get existing queue url: %w", getErr)
		}
		queueUrl = aws.ToString(getOut.QueueUrl)
		// Patch attributes that may have drifted (e.g. RedrivePolicy added later).
		if len(extraAttrs) > 0 {
			if _, setErr := client.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
				QueueUrl:   aws.String(queueUrl),
				Attributes: extraAttrs,
			}); setErr != nil {
				p.log.Warn("Failed to patch attributes on %s (non-fatal): %v", queueName, setErr)
			}
		}
	default:
		return "", "", fmt.Errorf("create queue: %w", err)
	}

	arnOut, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueUrl),
		AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return queueUrl, "", fmt.Errorf("fetch queue ARN: %w", err)
	}
	return queueUrl, arnOut.Attributes[string(sqsTypes.QueueAttributeNameQueueArn)], nil
}

// isSqsAlreadyExistsErr matches both QueueNameExists (same attrs) and
// QueueAlreadyExists (different attrs) — both mean "use the existing queue".
func isSqsAlreadyExistsErr(err error) bool {
	var existsSame *sqsTypes.QueueNameExists
	if errors.As(err, &existsSame) {
		return true
	}
	// AWS sometimes returns this as a generic error string for FIFO queues
	// with mismatched attrs. There is no typed error for it.
	return strings.Contains(err.Error(), "QueueAlreadyExists")
}

// ensureLambdaSQSTrigger wires the reval Lambda to the revalidation queue
// with batching, a small batching window, and reserved concurrency caps.
// The DLQ is enforced upstream on the queue itself (RedrivePolicy), so we
// rely on SQS for poison-message handling rather than Lambda's OnFailure.
func (p *AWSProvider) ensureLambdaSQSTrigger(ctx context.Context, client *lambda.Client, functionName, queueArn string) error {
	p.log.Info("Ensuring SQS trigger for %s...", functionName)

	if err := p.applyReservedConcurrency(ctx, client, functionName, revalReservedConcurrency); err != nil {
		p.log.Warn("Failed to set reserved concurrency on %s (non-fatal): %v", functionName, err)
	}

	listOutput, err := client.ListEventSourceMappings(ctx, &lambda.ListEventSourceMappingsInput{
		FunctionName:   aws.String(functionName),
		EventSourceArn: aws.String(queueArn),
	})
	if err != nil {
		return fmt.Errorf("failed to list event source mappings: %w", err)
	}
	if len(listOutput.EventSourceMappings) > 0 {
		p.verboseLog("  SQS trigger already exists.")
		return nil
	}

	var createErr error
	for attempt := 1; attempt <= 10; attempt++ {
		_, createErr = client.CreateEventSourceMapping(ctx, &lambda.CreateEventSourceMappingInput{
			FunctionName:                   aws.String(functionName),
			EventSourceArn:                 aws.String(queueArn),
			BatchSize:                      aws.Int32(10),
			MaximumBatchingWindowInSeconds: aws.Int32(5),
			Enabled:                        aws.Bool(true),
		})
		if createErr == nil {
			p.log.Info("SQS trigger created successfully for %s.", functionName)
			return nil
		}
		if !strings.Contains(createErr.Error(), "InvalidParameterValueException") {
			break // non-IAM-propagation error
		}
		p.verboseLog("Waiting for IAM permissions to propagate for SQS trigger (attempt %d/10)...", attempt)
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("failed to create event source mapping after retries: %w", createErr)
}

// applyReservedConcurrency caps the function's concurrent executions so a
// revalidation burst can't starve the main request Lambda for account-wide
// concurrency.
func (p *AWSProvider) applyReservedConcurrency(ctx context.Context, client *lambda.Client, functionName string, value int32) error {
	_, err := client.PutFunctionConcurrency(ctx, &lambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String(functionName),
		ReservedConcurrentExecutions: aws.Int32(value),
	})
	if err != nil {
		var notFound *lambdaTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil // function not yet created — DeployCompute will retry
		}
		return err
	}
	p.verboseLog("  Reserved concurrency on %s set to %d", functionName, value)
	return nil
}
