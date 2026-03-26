package serverless

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/sqs"
    sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
    "github.com/aws/aws-sdk-go-v2/service/lambda"
)

func (p *AWSProvider) ensureRevalidationQueueExists(ctx context.Context, appName string) (string, string, error) {
    client := sqs.NewFromConfig(p.cfg)
    queueName := fmt.Sprintf("nextdeploy-%s-revalidation.fifo", appName)

    p.log.Info("Ensuring Revalidation SQS Queue %s exists...", queueName)

    createOutput, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
        QueueName: aws.String(queueName),
        Attributes: map[string]string{
            "FifoQueue": "true",
            "ContentBasedDeduplication": "true",
        },
    })
    if err != nil {
        return "", "", fmt.Errorf("failed to create SQS queue: %w", err)
    }

    queueUrl := *createOutput.QueueUrl
    
    attrOutput, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
        QueueUrl: aws.String(queueUrl),
        AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameQueueArn},
    })
    if err != nil {
        return queueUrl, "", fmt.Errorf("failed to get SQS queue ARN: %w", err)
    }

    queueArn := attrOutput.Attributes[string(sqsTypes.QueueAttributeNameQueueArn)]
    return queueUrl, queueArn, nil
}

func (p *AWSProvider) ensureLambdaSQSTrigger(ctx context.Context, client *lambda.Client, functionName, queueArn string) error {
    p.log.Info("Ensuring SQS trigger for %s...", functionName)

    listOutput, err := client.ListEventSourceMappings(ctx, &lambda.ListEventSourceMappingsInput{
        FunctionName: aws.String(functionName),
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
            FunctionName: aws.String(functionName),
            EventSourceArn: aws.String(queueArn),
            BatchSize: aws.Int32(10),
            Enabled: aws.Bool(true),
        })
        
        if createErr == nil {
            p.log.Info("SQS trigger created successfully for %s.", functionName)
            return nil
        }
        
        if !strings.Contains(createErr.Error(), "InvalidParameterValueException") {
            break // Break on non-IAM propagation errors
        }
        
        p.verboseLog("Waiting for IAM permissions to propagate for SQS trigger (attempt %d/10)...", attempt)
        time.Sleep(5 * time.Second)
    }

    return fmt.Errorf("failed to create event source mapping after retries: %w", createErr)
}
