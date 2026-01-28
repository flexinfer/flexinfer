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
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

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
	}, handleRandomString)

	// uuid_v4
	server.AddTool(mcp.Tool{
		Name:        "uuid_v4",
		Description: "Generate a UUID v4",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleUUID)

	// hash_string
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
	}, handleHashString)

	// base64_encode
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
	}, handleBase64Encode)

	// base64_decode
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
	}, handleBase64Decode)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleRandomString(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	length := 16
	if l, ok := args["length"].(float64); ok {
		length = int(l)
	}

	// Validate length to prevent DoS
	if length <= 0 {
		return nil, fmt.Errorf("length must be positive")
	}
	if length > 10000 {
		return nil, fmt.Errorf("length exceeds maximum of 10000")
	}

	charsetType, _ := args["charset"].(string)

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
	text, _ := args["text"].(string)
	algo, _ := args["algorithm"].(string)

	var hash string
	switch algo {
	case "md5":
		sum := md5.Sum([]byte(text))
		hash = hex.EncodeToString(sum[:])
	case "sha256":
		sum := sha256.Sum256([]byte(text))
		hash = hex.EncodeToString(sum[:])
	default:
		// Default to sha256
		sum := sha256.Sum256([]byte(text))
		hash = hex.EncodeToString(sum[:])
		algo = "sha256"
	}

	return mcp.JSONResult(map[string]any{
		"text":      text,
		"hash":      hash,
		"algorithm": algo,
	})
}

func handleBase64Encode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	text, _ := args["text"].(string)
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return mcp.JSONResult(map[string]any{
		"original": text,
		"encoded":  encoded,
	})
}

func handleBase64Decode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	text, _ := args["text"].(string)
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return mcp.JSONResult(map[string]any{
		"encoded": text,
		"decoded": string(decoded),
	})
}
