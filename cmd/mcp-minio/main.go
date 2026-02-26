package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/portforward"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version   = "0.1.0"
	endpoint  = env.String("MINIO_ENDPOINT", "http://minio-service.news-analyzer.svc.cluster.local:80")
	accessKey = os.Getenv("MINIO_ACCESS_KEY")
	secretKey = os.Getenv("MINIO_SECRET_KEY")

	portForwarder = portforward.New(portforward.Config{
		Namespace:    "news-analyzer",
		Service:      "svc/minio-service",
		LocalPort:    9000,
		RemotePort:   80,
		HostPrefixes: []string{"minio-service"},
	}, env.Bool("MINIO_PORT_FORWARD", true))
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	defer cleanup()

	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-minio", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-minio")
	wrap := func(name string, h mcp.ToolHandler) mcp.ToolHandler {
		return mcpotel.TracedToolHandler(tracer, name, h)
	}
	logger.Info("starting server", "name", "mcp-minio", "version", version, "endpoint", endpoint)

	server := mcp.NewServer("mcp-minio", version)
	server.SetInstructions("MinIO/S3 file management")

	registerTools(server, wrap)

	return server.Run(ctx)
}

func cleanup() {
	portForwarder.Cleanup()
}

func registerTools(server *mcp.Server, wrap func(string, mcp.ToolHandler) mcp.ToolHandler) {
	server.AddTool(mcp.Tool{
		Name:        "minio_list_buckets",
		Description: "List all buckets",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, wrap("minio_list_buckets", handleListBuckets))

	server.AddTool(mcp.Tool{
		Name:        "minio_list_objects",
		Description: "List objects in a bucket",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket":    map[string]any{"type": "string"},
				"prefix":    map[string]any{"type": "string"},
				"recursive": map[string]any{"type": "boolean"},
				"max_keys":  map[string]any{"type": "integer"},
			},
			Required: []string{"bucket"},
		},
	}, wrap("minio_list_objects", handleListObjects))

	server.AddTool(mcp.Tool{
		Name:        "minio_get_object_text",
		Description: "Get object content as text",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket":      map[string]any{"type": "string"},
				"key":         map[string]any{"type": "string"},
				"bytes_limit": map[string]any{"type": "integer"},
			},
			Required: []string{"bucket", "key"},
		},
	}, wrap("minio_get_object_text", handleGetObjectText))

	server.AddTool(mcp.Tool{
		Name:        "minio_stat_object",
		Description: "Get object metadata",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket": map[string]any{"type": "string"},
				"key":    map[string]any{"type": "string"},
			},
			Required: []string{"bucket", "key"},
		},
	}, wrap("minio_stat_object", handleStatObject))

	server.AddTool(mcp.Tool{
		Name:        "minio_presign_get",
		Description: "Generate presigned GET URL",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket":          map[string]any{"type": "string"},
				"key":             map[string]any{"type": "string"},
				"expires_seconds": map[string]any{"type": "integer"},
			},
			Required: []string{"bucket", "key"},
		},
	}, wrap("minio_presign_get", handlePresignGet))

	server.AddTool(mcp.Tool{
		Name:        "minio_presign_put",
		Description: "Generate presigned PUT URL",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"bucket":          map[string]any{"type": "string"},
				"key":             map[string]any{"type": "string"},
				"expires_seconds": map[string]any{"type": "integer"},
				"content_type":    map[string]any{"type": "string"},
			},
			Required: []string{"bucket", "key"},
		},
	}, wrap("minio_presign_put", handlePresignPut))
}

// MinIO Client

var minioClient *minio.Client

func getClient() (*minio.Client, error) {
	if minioClient != nil {
		return minioClient, nil
	}

	if accessKey == "" || secretKey == "" {
		return nil, mcperror.NotConfigured("MINIO_ACCESS_KEY/MINIO_SECRET_KEY", "set MINIO_ACCESS_KEY and MINIO_SECRET_KEY environment variables")
	}

	effectiveEndpoint := portForwarder.EnsureRunning(endpoint)

	u, err := url.Parse(effectiveEndpoint)
	if err != nil {
		return nil, err
	}

	useSSL := u.Scheme == "https"
	host := u.Host

	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}

	minioClient = client
	return minioClient, nil
}

// Handlers

func handleListBuckets(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var res []map[string]any
	for _, b := range buckets {
		res = append(res, map[string]any{
			"name":         b.Name,
			"creationDate": b.CreationDate,
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"endpoint": endpoint,
		"buckets":  res,
		"count":    len(res),
	})
}

func handleListObjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	bucket := v.Required("bucket")
	prefix := v.String("prefix", "")
	recursive := v.Bool("recursive", false)
	maxKeys := v.Int("max_keys", 1000)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: recursive,
		MaxKeys:   maxKeys,
	}

	var objects []map[string]any
	for object := range client.ListObjects(ctx, bucket, opts) {
		if object.Err != nil {
			return mcp.ErrorResult(object.Err), nil
		}
		objects = append(objects, map[string]any{
			"key":          object.Key,
			"size":         object.Size,
			"etag":         object.ETag,
			"lastModified": object.LastModified,
			"storageClass": object.StorageClass,
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"bucket":  bucket,
		"prefix":  prefix,
		"objects": objects,
		"count":   len(objects),
	})
}

func handleGetObjectText(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	bucket := v.Required("bucket")
	key := v.Required("key")
	bytesLimit := v.Int("bytes_limit", 1024*1024) // 1MB default
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer obj.Close()

	buf := make([]byte, bytesLimit)
	n, err := obj.Read(buf)
	if err != nil && err != io.EOF {
		return mcp.ErrorResult(err), nil
	}

	text := string(buf[:n])
	truncated := false
	// Check if more data
	if _, err := obj.Read(make([]byte, 1)); err == nil {
		truncated = true
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"bucket":    bucket,
		"key":       key,
		"length":    len(text),
		"truncated": truncated,
		"text":      text,
	})
}

func handleStatObject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	bucket := v.Required("bucket")
	key := v.Required("key")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	info, err := client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"bucket": bucket,
		"key":    key,
		"stat": map[string]any{
			"size":         info.Size,
			"etag":         info.ETag,
			"contentType":  info.ContentType,
			"lastModified": info.LastModified,
			"metadata":     info.Metadata,
		},
	})
}

func handlePresignGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	bucket := v.Required("bucket")
	key := v.Required("key")
	expires := v.Int("expires_seconds", 3600)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	u, err := client.PresignedGetObject(ctx, bucket, key, time.Duration(expires)*time.Second, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"url":       u.String(),
		"expiresIn": int64(expires),
	})
}

func handlePresignPut(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	bucket := v.Required("bucket")
	key := v.Required("key")
	expires := v.Int("expires_seconds", 3600)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	u, err := client.PresignedPutObject(ctx, bucket, key, time.Duration(expires)*time.Second)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"url":       u.String(),
		"expiresIn": int64(expires),
	})
}
