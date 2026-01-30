// mcp-gcp provides MCP tools for Google Cloud Platform services access.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	functions "cloud.google.com/go/functions/apiv2"
	functionspb "cloud.google.com/go/functions/apiv2/functionspb"
	"cloud.google.com/go/storage"
	"gitlab.flexinfer.ai/libs/mcp-go"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

var (
	version = "0.1.0"

	gcpProject = os.Getenv("GCP_PROJECT")
	gcpRegion  = getEnv("GCP_REGION", "us-central1")
	gcpZone    = getEnv("GCP_ZONE", "us-central1-a")

	storageClient   *storage.Client
	instancesClient *compute.InstancesClient
	functionsClient *functions.FunctionClient
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func initGCP(ctx context.Context) error {
	var opts []option.ClientOption

	// Use credentials file if specified
	if credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credFile != "" {
		opts = append(opts, option.WithCredentialsFile(credFile))
	}

	var err error

	storageClient, err = storage.NewClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	instancesClient, err = compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("create instances client: %w", err)
	}

	functionsClient, err = functions.NewFunctionClient(ctx, opts...)
	if err != nil {
		return fmt.Errorf("create functions client: %w", err)
	}

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

	if err := initGCP(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "GCP init error: %v\n", err)
		os.Exit(1)
	}
	defer storageClient.Close()
	defer instancesClient.Close()
	defer functionsClient.Close()

	server := mcp.NewServer("mcp-gcp", version)
	server.SetInstructions("Google Cloud Platform tools. Uses Application Default Credentials or GOOGLE_APPLICATION_CREDENTIALS. Set GCP_PROJECT for project-scoped operations.")

	// Cloud Storage
	server.AddTool(mcp.Tool{
		Name:        "gcp_storage_list_buckets",
		Description: "List Cloud Storage buckets in the project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID (uses GCP_PROJECT env if not specified)",
				},
			},
		},
	}, handleStorageListBuckets)

	server.AddTool(mcp.Tool{
		Name:        "gcp_storage_list_objects",
		Description: "List objects in a Cloud Storage bucket",
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
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results (default: 100)",
				},
			},
			Required: []string{"bucket"},
		},
	}, handleStorageListObjects)

	server.AddTool(mcp.Tool{
		Name:        "gcp_storage_get_object",
		Description: "Get an object from Cloud Storage (metadata and content for text files)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket": map[string]any{
					"type":        "string",
					"description": "Bucket name",
				},
				"object": map[string]any{
					"type":        "string",
					"description": "Object name/path",
				},
				"max_size": map[string]any{
					"type":        "integer",
					"description": "Max content size to return in bytes (default: 1MB)",
				},
			},
			Required: []string{"bucket", "object"},
		},
	}, handleStorageGetObject)

	server.AddTool(mcp.Tool{
		Name:        "gcp_storage_object_metadata",
		Description: "Get object metadata without downloading content",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket": map[string]any{
					"type":        "string",
					"description": "Bucket name",
				},
				"object": map[string]any{
					"type":        "string",
					"description": "Object name/path",
				},
			},
			Required: []string{"bucket", "object"},
		},
	}, handleStorageObjectMetadata)

	// Compute Engine
	server.AddTool(mcp.Tool{
		Name:        "gcp_compute_list_instances",
		Description: "List Compute Engine instances",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID (uses GCP_PROJECT env if not specified)",
				},
				"zone": map[string]any{
					"type":        "string",
					"description": "Zone (uses GCP_ZONE env if not specified)",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter expression (e.g., 'status=RUNNING')",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results (default: 100)",
				},
			},
		},
	}, handleComputeListInstances)

	server.AddTool(mcp.Tool{
		Name:        "gcp_compute_get_instance",
		Description: "Get details about a specific Compute Engine instance",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID (uses GCP_PROJECT env if not specified)",
				},
				"zone": map[string]any{
					"type":        "string",
					"description": "Zone (uses GCP_ZONE env if not specified)",
				},
				"instance": map[string]any{
					"type":        "string",
					"description": "Instance name",
				},
			},
			Required: []string{"instance"},
		},
	}, handleComputeGetInstance)

	// Cloud Functions
	server.AddTool(mcp.Tool{
		Name:        "gcp_functions_list",
		Description: "List Cloud Functions (2nd gen)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID (uses GCP_PROJECT env if not specified)",
				},
				"region": map[string]any{
					"type":        "string",
					"description": "Region (uses GCP_REGION env if not specified, or '-' for all regions)",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results (default: 50)",
				},
			},
		},
	}, handleFunctionsList)

	server.AddTool(mcp.Tool{
		Name:        "gcp_functions_get",
		Description: "Get details about a Cloud Function",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID (uses GCP_PROJECT env if not specified)",
				},
				"region": map[string]any{
					"type":        "string",
					"description": "Region (uses GCP_REGION env if not specified)",
				},
				"function": map[string]any{
					"type":        "string",
					"description": "Function name",
				},
			},
			Required: []string{"function"},
		},
	}, handleFunctionsGet)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func getProject(args map[string]any) string {
	if p, ok := args["project"].(string); ok && p != "" {
		return p
	}
	return gcpProject
}

func getRegion(args map[string]any) string {
	if r, ok := args["region"].(string); ok && r != "" {
		return r
	}
	return gcpRegion
}

func getZone(args map[string]any) string {
	if z, ok := args["zone"].(string); ok && z != "" {
		return z
	}
	return gcpZone
}

func handleStorageListBuckets(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getProject(args)
	if project == "" {
		return mcp.ErrorResult(fmt.Errorf("project is required (set GCP_PROJECT env or pass project parameter)")), nil
	}

	it := storageClient.Buckets(ctx, project)
	buckets := []map[string]any{}

	for {
		bucket, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list buckets: %w", err)
		}

		buckets = append(buckets, map[string]any{
			"name":          bucket.Name,
			"location":      bucket.Location,
			"storage_class": bucket.StorageClass,
			"created":       bucket.Created.Format(time.RFC3339),
		})
	}

	return mcp.JSONResult(map[string]any{
		"project": project,
		"buckets": buckets,
		"count":   len(buckets),
	})
}

func handleStorageListObjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, ok := args["bucket"].(string)
	if !ok || bucket == "" {
		return mcp.ErrorResult(fmt.Errorf("bucket is required")), nil
	}

	query := &storage.Query{}
	if prefix, ok := args["prefix"].(string); ok && prefix != "" {
		query.Prefix = prefix
	}

	maxResults := 100
	if mr, ok := args["max_results"].(float64); ok && mr > 0 {
		maxResults = int(mr)
		if maxResults > 1000 {
			maxResults = 1000
		}
	}

	it := storageClient.Bucket(bucket).Objects(ctx, query)
	objects := []map[string]any{}

	for len(objects) < maxResults {
		obj, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}

		objects = append(objects, map[string]any{
			"name":          obj.Name,
			"size":          obj.Size,
			"content_type":  obj.ContentType,
			"updated":       obj.Updated.Format(time.RFC3339),
			"storage_class": obj.StorageClass,
		})
	}

	return mcp.JSONResult(map[string]any{
		"bucket":  bucket,
		"prefix":  query.Prefix,
		"objects": objects,
		"count":   len(objects),
	})
}

func handleStorageGetObject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, ok := args["bucket"].(string)
	if !ok || bucket == "" {
		return mcp.ErrorResult(fmt.Errorf("bucket is required")), nil
	}
	object, ok := args["object"].(string)
	if !ok || object == "" {
		return mcp.ErrorResult(fmt.Errorf("object is required")), nil
	}

	maxSize := int64(1024 * 1024) // 1MB default
	if ms, ok := args["max_size"].(float64); ok && ms > 0 {
		maxSize = int64(ms)
	}

	obj := storageClient.Bucket(bucket).Object(object)

	// Get metadata first
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get object attrs: %w", err)
	}

	response := map[string]any{
		"bucket":        bucket,
		"name":          attrs.Name,
		"size":          attrs.Size,
		"content_type":  attrs.ContentType,
		"updated":       attrs.Updated.Format(time.RFC3339),
		"created":       attrs.Created.Format(time.RFC3339),
		"storage_class": attrs.StorageClass,
		"md5":           attrs.MD5,
		"metadata":      attrs.Metadata,
	}

	// Only read content if it's small enough and likely text
	if attrs.Size <= maxSize {
		contentType := attrs.ContentType
		if strings.HasPrefix(contentType, "text/") ||
			strings.Contains(contentType, "json") ||
			strings.Contains(contentType, "xml") ||
			strings.Contains(contentType, "yaml") {

			reader, err := obj.NewReader(ctx)
			if err != nil {
				return nil, fmt.Errorf("create reader: %w", err)
			}
			defer reader.Close()

			content, err := io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("read content: %w", err)
			}
			response["content"] = string(content)
		}
	}

	return mcp.JSONResult(response)
}

func handleStorageObjectMetadata(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, ok := args["bucket"].(string)
	if !ok || bucket == "" {
		return mcp.ErrorResult(fmt.Errorf("bucket is required")), nil
	}
	object, ok := args["object"].(string)
	if !ok || object == "" {
		return mcp.ErrorResult(fmt.Errorf("object is required")), nil
	}

	attrs, err := storageClient.Bucket(bucket).Object(object).Attrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get object attrs: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"bucket":           bucket,
		"name":             attrs.Name,
		"size":             attrs.Size,
		"content_type":     attrs.ContentType,
		"updated":          attrs.Updated.Format(time.RFC3339),
		"created":          attrs.Created.Format(time.RFC3339),
		"storage_class":    attrs.StorageClass,
		"md5":              attrs.MD5,
		"crc32c":           attrs.CRC32C,
		"generation":       attrs.Generation,
		"metageneration":   attrs.Metageneration,
		"metadata":         attrs.Metadata,
		"content_encoding": attrs.ContentEncoding,
	})
}

func handleComputeListInstances(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getProject(args)
	if project == "" {
		return mcp.ErrorResult(fmt.Errorf("project is required (set GCP_PROJECT env or pass project parameter)")), nil
	}
	zone := getZone(args)

	req := &computepb.ListInstancesRequest{
		Project: project,
		Zone:    zone,
	}

	if filter, ok := args["filter"].(string); ok && filter != "" {
		req.Filter = &filter
	}

	maxResults := uint32(100)
	if mr, ok := args["max_results"].(float64); ok && mr > 0 {
		maxResults = uint32(mr)
		if maxResults > 500 {
			maxResults = 500
		}
	}
	req.MaxResults = &maxResults

	it := instancesClient.List(ctx, req)
	instances := []map[string]any{}

	for {
		instance, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list instances: %w", err)
		}

		// Get network info
		var privateIP, publicIP string
		if len(instance.NetworkInterfaces) > 0 {
			ni := instance.NetworkInterfaces[0]
			if ni.NetworkIP != nil {
				privateIP = *ni.NetworkIP
			}
			if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != nil {
				publicIP = *ni.AccessConfigs[0].NatIP
			}
		}

		instances = append(instances, map[string]any{
			"id":           instance.Id,
			"name":         instance.Name,
			"status":       instance.Status,
			"machine_type": extractResourceName(instance.MachineType),
			"zone":         extractResourceName(instance.Zone),
			"private_ip":   privateIP,
			"public_ip":    publicIP,
			"created":      instance.CreationTimestamp,
		})
	}

	return mcp.JSONResult(map[string]any{
		"project":   project,
		"zone":      zone,
		"instances": instances,
		"count":     len(instances),
	})
}

func handleComputeGetInstance(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getProject(args)
	if project == "" {
		return mcp.ErrorResult(fmt.Errorf("project is required (set GCP_PROJECT env or pass project parameter)")), nil
	}
	zone := getZone(args)
	instanceName, ok := args["instance"].(string)
	if !ok || instanceName == "" {
		return mcp.ErrorResult(fmt.Errorf("instance is required")), nil
	}

	instance, err := instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  project,
		Zone:     zone,
		Instance: instanceName,
	})
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}

	// Get network info
	networkInterfaces := []map[string]any{}
	for _, ni := range instance.NetworkInterfaces {
		niInfo := map[string]any{
			"network":    extractResourceName(ni.Network),
			"subnetwork": extractResourceName(ni.Subnetwork),
		}
		if ni.NetworkIP != nil {
			niInfo["private_ip"] = *ni.NetworkIP
		}
		if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != nil {
			niInfo["public_ip"] = *ni.AccessConfigs[0].NatIP
		}
		networkInterfaces = append(networkInterfaces, niInfo)
	}

	// Get disk info
	disks := []map[string]any{}
	for _, disk := range instance.Disks {
		diskInfo := map[string]any{
			"boot":        disk.Boot,
			"auto_delete": disk.AutoDelete,
			"mode":        disk.Mode,
		}
		if disk.Source != nil {
			diskInfo["source"] = extractResourceName(disk.Source)
		}
		if disk.DiskSizeGb != nil {
			diskInfo["size_gb"] = *disk.DiskSizeGb
		}
		disks = append(disks, diskInfo)
	}

	response := map[string]any{
		"id":                  instance.Id,
		"name":                instance.Name,
		"status":              instance.Status,
		"machine_type":        extractResourceName(instance.MachineType),
		"zone":                extractResourceName(instance.Zone),
		"created":             instance.CreationTimestamp,
		"network_interfaces":  networkInterfaces,
		"disks":               disks,
		"can_ip_forward":      instance.CanIpForward,
		"deletion_protection": instance.DeletionProtection,
	}

	if instance.Description != nil {
		response["description"] = *instance.Description
	}
	if instance.Labels != nil {
		response["labels"] = instance.Labels
	}

	return mcp.JSONResult(response)
}

func handleFunctionsList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getProject(args)
	if project == "" {
		return mcp.ErrorResult(fmt.Errorf("project is required (set GCP_PROJECT env or pass project parameter)")), nil
	}
	region := getRegion(args)

	parent := fmt.Sprintf("projects/%s/locations/%s", project, region)

	req := &functionspb.ListFunctionsRequest{
		Parent: parent,
	}

	maxResults := 50
	if mr, ok := args["max_results"].(float64); ok && mr > 0 {
		maxResults = int(mr)
		if maxResults > 500 {
			maxResults = 500
		}
	}
	req.PageSize = int32(maxResults)

	it := functionsClient.ListFunctions(ctx, req)
	functions := []map[string]any{}

	for len(functions) < maxResults {
		fn, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list functions: %w", err)
		}

		fnInfo := map[string]any{
			"name":        extractResourceName(&fn.Name),
			"state":       fn.State.String(),
			"environment": fn.Environment.String(),
			"update_time": fn.UpdateTime.AsTime().Format(time.RFC3339),
		}

		if fn.Description != "" {
			fnInfo["description"] = fn.Description
		}
		if fn.BuildConfig != nil {
			fnInfo["runtime"] = fn.BuildConfig.Runtime
			fnInfo["entry_point"] = fn.BuildConfig.EntryPoint
		}
		if fn.ServiceConfig != nil {
			fnInfo["memory_mb"] = fn.ServiceConfig.AvailableMemory
			fnInfo["timeout_seconds"] = fn.ServiceConfig.TimeoutSeconds
		}

		functions = append(functions, fnInfo)
	}

	return mcp.JSONResult(map[string]any{
		"project":   project,
		"region":    region,
		"functions": functions,
		"count":     len(functions),
	})
}

func handleFunctionsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getProject(args)
	if project == "" {
		return mcp.ErrorResult(fmt.Errorf("project is required (set GCP_PROJECT env or pass project parameter)")), nil
	}
	region := getRegion(args)
	functionName, ok := args["function"].(string)
	if !ok || functionName == "" {
		return mcp.ErrorResult(fmt.Errorf("function is required")), nil
	}

	name := fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, region, functionName)

	fn, err := functionsClient.GetFunction(ctx, &functionspb.GetFunctionRequest{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("get function: %w", err)
	}

	response := map[string]any{
		"name":        extractResourceName(&fn.Name),
		"state":       fn.State.String(),
		"environment": fn.Environment.String(),
		"update_time": fn.UpdateTime.AsTime().Format(time.RFC3339),
		"labels":      fn.Labels,
	}

	if fn.Description != "" {
		response["description"] = fn.Description
	}

	if fn.BuildConfig != nil {
		response["build"] = map[string]any{
			"runtime":     fn.BuildConfig.Runtime,
			"entry_point": fn.BuildConfig.EntryPoint,
			"source":      fn.BuildConfig.Source,
		}
	}

	if fn.ServiceConfig != nil {
		response["service"] = map[string]any{
			"memory":             fn.ServiceConfig.AvailableMemory,
			"timeout_seconds":    fn.ServiceConfig.TimeoutSeconds,
			"max_instance_count": fn.ServiceConfig.MaxInstanceCount,
			"min_instance_count": fn.ServiceConfig.MinInstanceCount,
			"service_account":    fn.ServiceConfig.ServiceAccountEmail,
			"ingress_settings":   fn.ServiceConfig.IngressSettings.String(),
			"vpc_connector":      fn.ServiceConfig.VpcConnector,
		}
		if fn.ServiceConfig.Uri != "" {
			response["url"] = fn.ServiceConfig.Uri
		}
	}

	if fn.EventTrigger != nil {
		response["trigger"] = map[string]any{
			"trigger_region": fn.EventTrigger.TriggerRegion,
			"event_type":     fn.EventTrigger.EventType,
			"pubsub_topic":   fn.EventTrigger.PubsubTopic,
		}
	}

	return mcp.JSONResult(response)
}

// extractResourceName extracts the last component from a GCP resource path
func extractResourceName(path *string) string {
	if path == nil {
		return ""
	}
	parts := strings.Split(*path, "/")
	return parts[len(parts)-1]
}
