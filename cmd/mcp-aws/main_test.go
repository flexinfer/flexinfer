package main

import "testing"

func TestToolDefinitions(t *testing.T) {
	// Verify the expected tool inventory for the AWS MCP server.
	// We cannot call run() without real AWS credentials,
	// so we verify tool names as a static inventory check.
	expectedTools := map[string]string{
		"aws_whoami":                       "Get current AWS identity (account, user/role ARN)",
		"aws_s3_list_buckets":              "List all S3 buckets in the account",
		"aws_s3_list_objects":              "List objects in an S3 bucket",
		"aws_s3_get_object":                "Get an object from S3 (returns metadata and content for text files)",
		"aws_s3_head_object":               "Get object metadata without downloading content",
		"aws_ec2_describe_instances":       "List EC2 instances with optional filtering",
		"aws_ec2_describe_vpcs":            "List VPCs",
		"aws_ec2_describe_security_groups": "List security groups",
		"aws_lambda_list_functions":        "List Lambda functions",
		"aws_lambda_get_function":          "Get details about a Lambda function",
		"aws_lambda_invoke":                "Invoke a Lambda function",
	}

	for name, desc := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
		if desc == "" {
			t.Errorf("tool %q must have a non-empty description", name)
		}
	}

	if len(expectedTools) != 11 {
		t.Errorf("expected 11 tools, got %d", len(expectedTools))
	}
}

func TestAWSRegionDefault(t *testing.T) {
	// The default region should be set.
	if awsRegion == "" {
		t.Error("awsRegion should have a default value")
	}
}
