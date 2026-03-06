package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func (p *AWSProvider) ensureExecutionRoleExists(ctx context.Context) (string, error) {
	client := iam.NewFromConfig(p.cfg)
	roleName := "nextdeploy-serverless-role"

	p.log.Info("Checking for IAM execution role: %s...", roleName)
	getOutput, err := client.GetRole(ctx, &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	})

	if err == nil {
		p.log.Info("IAM execution role found: %s", *getOutput.Role.Arn)
		return *getOutput.Role.Arn, nil
	}

	var noSuchEntity *types.NoSuchEntityException
	if !errors.As(err, &noSuchEntity) {
		return "", fmt.Errorf("failed to check for IAM role: %w", err)
	}

	p.log.Info("IAM role %s not found, creating one...", roleName)

	trustPolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Principal": map[string]interface{}{
					"Service": "lambda.amazonaws.com",
				},
				"Action": "sts:AssumeRole",
			},
		},
	}
	policyJSON, _ := json.Marshal(trustPolicy)

	createOutput, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(string(policyJSON)),
		Description:              aws.String("Managed by NextDeploy for Serverless Lambda execution"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create IAM role: %w", err)
	}

	p.log.Info("Attaching managed policies to role %s...", roleName)
	managedPolicies := []string{
		"arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole",
	}

	for _, policyArn := range managedPolicies {
		_, err = client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyArn),
		})
		if err != nil {
			p.log.Warn("Failed to attach policy %s: %v", policyArn, err)
		}
	}

	// Build and attach scoped inline policy
	// TODO: Verify this policy covers all access paths Lambda needs in different regions/contexts
	inlinePolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{"s3:GetObject", "s3:ListBucket"},
				"Resource": []string{
					fmt.Sprintf("arn:aws:s3:::nextdeploy-%s-*", p.accountID),
					fmt.Sprintf("arn:aws:s3:::nextdeploy-%s-*/*", p.accountID),
				},
			},
			{
				"Effect": "Allow",
				"Action": []string{
					"secretsmanager:GetSecretValue",
					"secretsmanager:DescribeSecret",
				},
				"Resource": fmt.Sprintf("arn:aws:secretsmanager:*:%s:secret:nextdeploy/*", p.accountID),
			},
		},
	}
	inlinePolicyJSON, _ := json.Marshal(inlinePolicy)
	_, err = client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String("nextdeploy-scoped-access"),
		PolicyDocument: aws.String(string(inlinePolicyJSON)),
	})
	if err != nil {
		p.log.Warn("Failed to attach scoped inline policy: %v", err)
	}

	p.log.Info("IAM role created successfully: %s", *createOutput.Role.Arn)
	return *createOutput.Role.Arn, nil
}
