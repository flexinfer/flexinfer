package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/mcp"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	version   = "0.1.0"
	endpoint  = getEnv("MINIO_ENDPOINT", "http://minio-service.news-analyzer.svc.cluster.local:80")
	accessKey = os.Getenv("MINIO_ACCESS_KEY")
	secretKey = os.Getenv("MINIO_SECRET_KEY")

	portForward    = getEnvBool("MINIO_PORT_FORWARD", true)
	portForwardCmd *exec.Cmd
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		v = strings.ToLower(v)
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-minio", version)
	server.SetInstructions("MinIO/S3 file management")

	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cleanup()
		os.Exit(1)
	}
	cleanup()
}

func cleanup() {
	if portForwardCmd != nil && portForwardCmd.Process != nil {
		portForwardCmd.Process.Kill()
	}
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "minio_list_buckets",
		Description: "List all buckets",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleListBuckets)

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
	}, handleListObjects)

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
	}, handleGetObjectText)

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
	}, handleStatObject)

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
	}, handlePresignGet)

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
	}, handlePresignPut)
}

// MinIO Client

var minioClient *minio.Client

func getClient() (*minio.Client, error) {
	if minioClient != nil {
		return minioClient, nil
	}

	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("MINIO_ACCESS_KEY / MINIO_SECRET_KEY not set")
	}

	maybeStartPortForward()

	u, err := url.Parse(endpoint)
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

func maybeStartPortForward() {
	if !portForward {
		return
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return
	}

	host := u.Hostname()
	needsPF := strings.HasSuffix(host, ".svc.cluster.local") || strings.HasSuffix(host, ".svc") || strings.HasPrefix(host, "minio-service")

	if !needsPF {
		return
	}

	if portForwardCmd != nil {
		if portForwardCmd.ProcessState == nil {
			return // Still running
		}
	}

	// Start port-forward
	// kubectl -n news-analyzer port-forward svc/minio-service 9000:80
	cmd := exec.Command("kubectl", "-n", "news-analyzer", "port-forward", "svc/minio-service", "9000:80")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err == nil {
		portForwardCmd = cmd
		endpoint = "http://127.0.0.1:9000"
		time.Sleep(500 * time.Millisecond)
	}
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
	bucket, _ := args["bucket"].(string)
	prefix, _ := args["prefix"].(string)
	recursive, _ := args["recursive"].(bool)
	maxKeys, _ := args["max_keys"].(float64)
	if maxKeys == 0 {
		maxKeys = 1000
	}

	client, err := getClient()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: recursive,
		MaxKeys:   int(maxKeys),
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
	bucket, _ := args["bucket"].(string)
	key, _ := args["key"].(string)
	bytesLimit, _ := args["bytes_limit"].(float64)
	if bytesLimit == 0 {
		bytesLimit = 1024 * 1024 // 1MB
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

	buf := make([]byte, int(bytesLimit))
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
	bucket, _ := args["bucket"].(string)
	key, _ := args["key"].(string)

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
	bucket, _ := args["bucket"].(string)
	key, _ := args["key"].(string)
	expires, _ := args["expires_seconds"].(float64)
	if expires == 0 {
		expires = 3600
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
		"expiresIn": expires,
	})
}

func handlePresignPut(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	bucket, _ := args["bucket"].(string)
	key, _ := args["key"].(string)
	expires, _ := args["expires_seconds"].(float64)
	if expires == 0 {
		expires = 3600
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
		"expiresIn": expires,
	})
}
