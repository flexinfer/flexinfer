// mcp-aws provides MCP tools for AWS services access.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version = "0.1.0"

	awsRegion = getEnv("AWS_REGION", "us-east-1")

	awsCfg    aws.Config
	s3Client  *s3.Client
	ec2Client *ec2.Client
	stsClient *sts.Client
	lmdClient *lambda.Client
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func initAWS(ctx context.Context) error {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsRegion))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	awsCfg = cfg
	s3Client = s3.NewFromConfig(cfg)
	ec2Client = ec2.NewFromConfig(cfg)
	stsClient = sts.NewFromConfig(cfg)
	lmdClient = lambda.NewFromConfig(cfg)
	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	if err := initAWS(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "AWS init error: %v\n", err)
		os.Exit(1)
	}

	server := mcp.NewServer("mcp-aws", version)
	server.SetInstructions("AWS services tools. Uses standard AWS credential chain (env vars, shared credentials, IAM role). Set AWS_REGION to change region.")

	// Identity
	server.AddTool(mcp.Tool{
		Name:        "aws_whoami",
		Description: "Get current AWS identity (account, user/role ARN)",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleWhoAmI)

	// S3
	server.AddTool(mcp.Tool{
		Name:        "aws_s3_list_buckets",
		Description: "List all S3 buckets in the account",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleS3ListBuckets)

	server.AddTool(mcp.Tool{
		Name:        "aws_s3_list_objects",
		Description: "List objects in an S3 bucket",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket": map[string]any{
					"type":        "string",
					"description": "Bucket name",
				},
				"prefix": map[string]any{
					"type":        "string",
					"description": "Object prefix filter",
				},
				"max_keys": map[string]any{
					"type":        "integer",
					"description": "Maximum number of keys to return (default: 100, max: 1000)",
				},
			},
			Required: []string{"bucket"},
		},
	}, handleS3ListObjects)

	server.AddTool(mcp.Tool{
		Name:        "aws_s3_get_object",
		Description: "Get an object from S3 (returns metadata and content for text files)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket": map[string]any{
					"type":        "string",
					"description": "Bucket name",
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Object key",
				},
				"max_size": map[string]any{
					"type":        "integer",
					"description": "Max content size to return in bytes (default: 1MB)",
				},
			},
			Required: []string{"bucket", "key"},
		},
	}, handleS3GetObject)

	server.AddTool(mcp.Tool{
		Name:        "aws_s3_head_object",
		Description: "Get object metadata without downloading content",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket": map[string]any{
					"type":        "string",
					"description": "Bucket name",
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Object key",
				},
			},
			Required: []string{"bucket", "key"},
		},
	}, handleS3HeadObject)

	// EC2
	server.AddTool(mcp.Tool{
		Name:        "aws_ec2_describe_instances",
		Description: "List EC2 instances with optional filtering",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"instance_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Specific instance IDs to describe",
				},
				"filters": map[string]any{
					"type":        "object",
					"description": "Filters as key-value pairs (e.g., {\"instance-state-name\": \"running\"})",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results (default: 100)",
				},
			},
		},
	}, handleEC2DescribeInstances)

	server.AddTool(mcp.Tool{
		Name:        "aws_ec2_describe_vpcs",
		Description: "List VPCs",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"vpc_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Specific VPC IDs to describe",
				},
			},
		},
	}, handleEC2DescribeVPCs)

	server.AddTool(mcp.Tool{
		Name:        "aws_ec2_describe_security_groups",
		Description: "List security groups",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"group_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Specific security group IDs",
				},
				"vpc_id": map[string]any{
					"type":        "string",
					"description": "Filter by VPC ID",
				},
			},
		},
	}, handleEC2DescribeSecurityGroups)

	// Lambda
	server.AddTool(mcp.Tool{
		Name:        "aws_lambda_list_functions",
		Description: "List Lambda functions",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"max_items": map[string]any{
					"type":        "integer",
					"description": "Maximum number of functions to return (default: 50)",
				},
			},
		},
	}, handleLambdaListFunctions)

	server.AddTool(mcp.Tool{
		Name:        "aws_lambda_get_function",
		Description: "Get details about a Lambda function",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"function_name": map[string]any{
					"type":        "string",
					"description": "Function name or ARN",
				},
			},
			Required: []string{"function_name"},
		},
	}, handleLambdaGetFunction)

	server.AddTool(mcp.Tool{
		Name:        "aws_lambda_invoke",
		Description: "Invoke a Lambda function",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"function_name": map[string]any{
					"type":        "string",
					"description": "Function name or ARN",
				},
				"payload": map[string]any{
					"type":        "object",
					"description": "JSON payload to send to the function",
				},
				"invocation_type": map[string]any{
					"type":        "string",
					"enum":        []string{"RequestResponse", "Event", "DryRun"},
					"description": "Invocation type (default: RequestResponse for sync)",
				},
			},
			Required: []string{"function_name"},
		},
	}, handleLambdaInvoke)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func handleWhoAmI(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("get caller identity: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"account": aws.ToString(result.Account),
		"arn":     aws.ToString(result.Arn),
		"user_id": aws.ToString(result.UserId),
		"region":  awsRegion,
	})
}

func handleS3ListBuckets(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := s3Client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}

	buckets := make([]map[string]any, 0, len(result.Buckets))
	for _, b := range result.Buckets {
		buckets = append(buckets, map[string]any{
			"name":          aws.ToString(b.Name),
			"creation_date": b.CreationDate.Format(time.RFC3339),
		})
	}

	return mcp.JSONResult(map[string]any{
		"buckets": buckets,
		"count":   len(buckets),
	})
}

func handleS3ListObjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, ok := args["bucket"].(string)
	if !ok || bucket == "" {
		return mcp.ErrorResult(fmt.Errorf("bucket is required")), nil
	}

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		MaxKeys: aws.Int32(100),
	}

	if prefix, ok := args["prefix"].(string); ok && prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if maxKeys, ok := args["max_keys"].(float64); ok && maxKeys > 0 {
		if maxKeys > 1000 {
			maxKeys = 1000
		}
		input.MaxKeys = aws.Int32(int32(maxKeys))
	}

	result, err := s3Client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	objects := make([]map[string]any, 0, len(result.Contents))
	for _, obj := range result.Contents {
		objects = append(objects, map[string]any{
			"key":           aws.ToString(obj.Key),
			"size":          obj.Size,
			"last_modified": obj.LastModified.Format(time.RFC3339),
			"storage_class": string(obj.StorageClass),
		})
	}

	return mcp.JSONResult(map[string]any{
		"bucket":       bucket,
		"prefix":       aws.ToString(input.Prefix),
		"objects":      objects,
		"count":        len(objects),
		"is_truncated": result.IsTruncated,
	})
}

func handleS3GetObject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, ok := args["bucket"].(string)
	if !ok || bucket == "" {
		return mcp.ErrorResult(fmt.Errorf("bucket is required")), nil
	}
	key, ok := args["key"].(string)
	if !ok || key == "" {
		return mcp.ErrorResult(fmt.Errorf("key is required")), nil
	}

	maxSize := int64(1024 * 1024) // 1MB default
	if ms, ok := args["max_size"].(float64); ok && ms > 0 {
		maxSize = int64(ms)
	}

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer result.Body.Close()

	response := map[string]any{
		"bucket":         bucket,
		"key":            key,
		"content_type":   aws.ToString(result.ContentType),
		"content_length": result.ContentLength,
		"last_modified":  result.LastModified.Format(time.RFC3339),
		"etag":           aws.ToString(result.ETag),
	}

	// Only read content if it's small enough and likely text
	if result.ContentLength != nil && *result.ContentLength <= maxSize {
		contentType := aws.ToString(result.ContentType)
		if strings.HasPrefix(contentType, "text/") ||
			strings.Contains(contentType, "json") ||
			strings.Contains(contentType, "xml") ||
			strings.Contains(contentType, "yaml") {
			buf := make([]byte, *result.ContentLength)
			n, err := result.Body.Read(buf)
			if err == nil || n > 0 {
				response["content"] = string(buf[:n])
			}
		}
	}

	return mcp.JSONResult(response)
}

func handleS3HeadObject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, ok := args["bucket"].(string)
	if !ok || bucket == "" {
		return mcp.ErrorResult(fmt.Errorf("bucket is required")), nil
	}
	key, ok := args["key"].(string)
	if !ok || key == "" {
		return mcp.ErrorResult(fmt.Errorf("key is required")), nil
	}

	result, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("head object: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"bucket":         bucket,
		"key":            key,
		"content_type":   aws.ToString(result.ContentType),
		"content_length": result.ContentLength,
		"last_modified":  result.LastModified.Format(time.RFC3339),
		"etag":           aws.ToString(result.ETag),
		"storage_class":  string(result.StorageClass),
		"metadata":       result.Metadata,
	})
}

func handleEC2DescribeInstances(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	input := &ec2.DescribeInstancesInput{
		MaxResults: aws.Int32(100),
	}

	if ids, ok := args["instance_ids"].([]any); ok && len(ids) > 0 {
		instanceIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			if s, ok := id.(string); ok {
				instanceIDs = append(instanceIDs, s)
			}
		}
		input.InstanceIds = instanceIDs
	}

	if filters, ok := args["filters"].(map[string]any); ok && len(filters) > 0 {
		ec2Filters := make([]ec2types.Filter, 0, len(filters))
		for k, v := range filters {
			if s, ok := v.(string); ok {
				ec2Filters = append(ec2Filters, ec2types.Filter{
					Name:   aws.String(k),
					Values: []string{s},
				})
			}
		}
		input.Filters = ec2Filters
	}

	if maxResults, ok := args["max_results"].(float64); ok && maxResults > 0 {
		if maxResults > 1000 {
			maxResults = 1000
		}
		input.MaxResults = aws.Int32(int32(maxResults))
	}

	result, err := ec2Client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describe instances: %w", err)
	}

	instances := []map[string]any{}
	for _, reservation := range result.Reservations {
		for _, inst := range reservation.Instances {
			// Get name tag
			name := ""
			for _, tag := range inst.Tags {
				if aws.ToString(tag.Key) == "Name" {
					name = aws.ToString(tag.Value)
					break
				}
			}

			instances = append(instances, map[string]any{
				"instance_id":    aws.ToString(inst.InstanceId),
				"name":           name,
				"instance_type":  string(inst.InstanceType),
				"state":          string(inst.State.Name),
				"private_ip":     aws.ToString(inst.PrivateIpAddress),
				"public_ip":      aws.ToString(inst.PublicIpAddress),
				"vpc_id":         aws.ToString(inst.VpcId),
				"subnet_id":      aws.ToString(inst.SubnetId),
				"launch_time":    inst.LaunchTime.Format(time.RFC3339),
				"az":             aws.ToString(inst.Placement.AvailabilityZone),
				"ami_id":         aws.ToString(inst.ImageId),
				"key_name":       aws.ToString(inst.KeyName),
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"instances": instances,
		"count":     len(instances),
	})
}

func handleEC2DescribeVPCs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	input := &ec2.DescribeVpcsInput{}

	if ids, ok := args["vpc_ids"].([]any); ok && len(ids) > 0 {
		vpcIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			if s, ok := id.(string); ok {
				vpcIDs = append(vpcIDs, s)
			}
		}
		input.VpcIds = vpcIDs
	}

	result, err := ec2Client.DescribeVpcs(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describe vpcs: %w", err)
	}

	vpcs := make([]map[string]any, 0, len(result.Vpcs))
	for _, vpc := range result.Vpcs {
		name := ""
		for _, tag := range vpc.Tags {
			if aws.ToString(tag.Key) == "Name" {
				name = aws.ToString(tag.Value)
				break
			}
		}

		vpcs = append(vpcs, map[string]any{
			"vpc_id":     aws.ToString(vpc.VpcId),
			"name":       name,
			"cidr_block": aws.ToString(vpc.CidrBlock),
			"state":      string(vpc.State),
			"is_default": vpc.IsDefault,
		})
	}

	return mcp.JSONResult(map[string]any{
		"vpcs":  vpcs,
		"count": len(vpcs),
	})
}

func handleEC2DescribeSecurityGroups(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	input := &ec2.DescribeSecurityGroupsInput{}

	if ids, ok := args["group_ids"].([]any); ok && len(ids) > 0 {
		groupIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			if s, ok := id.(string); ok {
				groupIDs = append(groupIDs, s)
			}
		}
		input.GroupIds = groupIDs
	}

	if vpcID, ok := args["vpc_id"].(string); ok && vpcID != "" {
		input.Filters = []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		}
	}

	result, err := ec2Client.DescribeSecurityGroups(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describe security groups: %w", err)
	}

	groups := make([]map[string]any, 0, len(result.SecurityGroups))
	for _, sg := range result.SecurityGroups {
		groups = append(groups, map[string]any{
			"group_id":    aws.ToString(sg.GroupId),
			"group_name":  aws.ToString(sg.GroupName),
			"description": aws.ToString(sg.Description),
			"vpc_id":      aws.ToString(sg.VpcId),
			"inbound_rules_count":  len(sg.IpPermissions),
			"outbound_rules_count": len(sg.IpPermissionsEgress),
		})
	}

	return mcp.JSONResult(map[string]any{
		"security_groups": groups,
		"count":           len(groups),
	})
}

func handleLambdaListFunctions(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	input := &lambda.ListFunctionsInput{
		MaxItems: aws.Int32(50),
	}

	if maxItems, ok := args["max_items"].(float64); ok && maxItems > 0 {
		if maxItems > 1000 {
			maxItems = 1000
		}
		input.MaxItems = aws.Int32(int32(maxItems))
	}

	result, err := lmdClient.ListFunctions(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list functions: %w", err)
	}

	functions := make([]map[string]any, 0, len(result.Functions))
	for _, fn := range result.Functions {
		functions = append(functions, map[string]any{
			"function_name": aws.ToString(fn.FunctionName),
			"runtime":       string(fn.Runtime),
			"handler":       aws.ToString(fn.Handler),
			"memory_size":   fn.MemorySize,
			"timeout":       fn.Timeout,
			"last_modified": aws.ToString(fn.LastModified),
			"code_size":     fn.CodeSize,
			"description":   aws.ToString(fn.Description),
		})
	}

	return mcp.JSONResult(map[string]any{
		"functions": functions,
		"count":     len(functions),
	})
}

func handleLambdaGetFunction(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	functionName, ok := args["function_name"].(string)
	if !ok || functionName == "" {
		return mcp.ErrorResult(fmt.Errorf("function_name is required")), nil
	}

	result, err := lmdClient.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		return nil, fmt.Errorf("get function: %w", err)
	}

	config := result.Configuration
	response := map[string]any{
		"function_name":  aws.ToString(config.FunctionName),
		"function_arn":   aws.ToString(config.FunctionArn),
		"runtime":        string(config.Runtime),
		"handler":        aws.ToString(config.Handler),
		"role":           aws.ToString(config.Role),
		"memory_size":    config.MemorySize,
		"timeout":        config.Timeout,
		"last_modified":  aws.ToString(config.LastModified),
		"code_size":      config.CodeSize,
		"description":    aws.ToString(config.Description),
		"state":          string(config.State),
		"code_sha256":    aws.ToString(config.CodeSha256),
	}

	if result.Code != nil {
		response["code_location"] = aws.ToString(result.Code.Location)
		response["code_repository_type"] = aws.ToString(result.Code.RepositoryType)
	}

	return mcp.JSONResult(response)
}

func handleLambdaInvoke(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	functionName, ok := args["function_name"].(string)
	if !ok || functionName == "" {
		return mcp.ErrorResult(fmt.Errorf("function_name is required")), nil
	}

	input := &lambda.InvokeInput{
		FunctionName: aws.String(functionName),
	}

	if payload, ok := args["payload"].(map[string]any); ok {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		input.Payload = payloadBytes
	}

	if invocationType, ok := args["invocation_type"].(string); ok {
		input.InvocationType = lambdatypes.InvocationType(invocationType)
	}

	result, err := lmdClient.Invoke(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("invoke function: %w", err)
	}

	response := map[string]any{
		"function_name":   functionName,
		"status_code":     result.StatusCode,
		"executed_version": aws.ToString(result.ExecutedVersion),
	}

	if result.FunctionError != nil {
		response["function_error"] = aws.ToString(result.FunctionError)
	}

	if len(result.Payload) > 0 {
		var payloadResult any
		if err := json.Unmarshal(result.Payload, &payloadResult); err == nil {
			response["response"] = payloadResult
		} else {
			response["response_raw"] = string(result.Payload)
		}
	}

	return mcp.JSONResult(response)
}
