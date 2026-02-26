// mcp-crypto provides cryptographic and encoding utilities.
package main

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"

	"github.com/google/uuid"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-crypto",

		logger)
	if err != nil {
		logger.Warn("OTel tracer init failed",

			"error", err,
		)
	}
	defer func() {
		_ =
			shutdownTracer(ctx)
	}()
	tracer := mcpotel.Tracer(tp, "mcp-crypto")

	logger.Info("starting server", "name", "mcp-crypto", "version", version)

	server := mcp.NewServer("mcp-crypto", version)
	server.SetInstructions("Cryptographic and encoding utilities. Tools: random_string, uuid_v4, hash_string, base64_encode, base64_decode")

	// random_string
	server.AddTool(mcp.Tool{
		Name:        "random_string",
		Description: "Generate a cryptographically secure random string",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"length": map[string]any{
					"type":        "number",
					"description": "Length of the string (default: 16)",
				},
				"charset": map[string]any{
					"type":        "string",
					"description": "Charset to use (alphanumeric, alpha, numeric, hex). Default: alphanumeric",
				},
			},
		},
	}, mcpotel.TracedToolHandler(

		// uuid_v4
		tracer, "random_string", handleRandomString))

	server.AddTool(mcp.Tool{
		Name:        "uuid_v4",
		Description: "Generate a UUID v4",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(

		// hash_string
		tracer, "uuid_v4", handleUUID))

	server.AddTool(mcp.Tool{
		Name:        "hash_string",
		Description: "Hash a string using MD5 or SHA256",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Text to hash",
				},
				"algorithm": map[string]any{
					"type":        "string",
					"enum":        []string{"md5", "sha256"},
					"description": "Hash algorithm",
				},
			},
			Required: []string{"text"},
		},
	}, mcpotel.TracedToolHandler(

		// base64_encode
		tracer, "hash_string", handleHashString))

	server.AddTool(mcp.Tool{
		Name:        "base64_encode",
		Description: "Encode text to Base64",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Text to encode",
				},
			},
			Required: []string{"text"},
		},
	}, mcpotel.TracedToolHandler(

		// base64_decode
		tracer, "base64_encode", handleBase64Encode))

	server.AddTool(mcp.Tool{
		Name:        "base64_decode",
		Description: "Decode Base64 text",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Base64 text to decode",
				},
			},
			Required: []string{"text"},
		},
	}, mcpotel.TracedToolHandler(tracer, "base64_decode", handleBase64Decode))

	return server.Run(ctx)
}

func handleRandomString(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	length := v.IntRange("length", 16, 1, 10000)
	charsetType := v.String("charset", "alphanumeric")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	const (
		alphaOps    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
		numOps      = "0123456789"
		hexOps      = "0123456789abcdef"
		alphaNumOps = alphaOps + numOps
	)

	var charset string
	switch charsetType {
	case "alpha":
		charset = alphaOps
	case "numeric":
		charset = numOps
	case "hex":
		charset = hexOps
	default: // alphanumeric
		charset = alphaNumOps
	}

	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return nil, fmt.Errorf("secure random generation failed: %w", err)
		}
		b[i] = charset[n.Int64()]
	}

	return mcp.JSONResult(map[string]any{
		"result":  string(b),
		"length":  length,
		"charset": charsetType,
	})
}

func handleUUID(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	u := uuid.New()
	return mcp.JSONResult(map[string]any{
		"uuid": u.String(),
	})
}

func handleHashString(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	text := v.Required("text")
	algo := v.Enum("algorithm", "sha256", "md5", "sha256")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var hash string
	switch algo {
	case "md5":
		sum := md5.Sum([]byte(text))
		hash = hex.EncodeToString(sum[:])
	default: // sha256
		sum := sha256.Sum256([]byte(text))
		hash = hex.EncodeToString(sum[:])
	}

	return mcp.JSONResult(map[string]any{
		"text":      text,
		"hash":      hash,
		"algorithm": algo,
	})
}

func handleBase64Encode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	text := v.Required("text")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return mcp.JSONResult(map[string]any{
		"original": text,
		"encoded":  encoded,
	})
}

func handleBase64Decode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	text := v.Required("text")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("decode: %w", err)), nil
	}
	return mcp.JSONResult(map[string]any{
		"encoded": text,
		"decoded": string(decoded),
	})
}
