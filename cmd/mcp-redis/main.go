// mcp-redis is an MCP server for Redis cache inspection and monitoring.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/validate"
	"github.com/redis/go-redis/v9"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

type redisServer struct {
	client *redis.Client
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse REDIS_URL: %v\n", err)
		os.Exit(1)
	}

	client := redis.NewClient(opts)
	defer client.Close()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to Redis: %v\n", err)
		os.Exit(1)
	}

	rs := &redisServer{client: client}

	server := mcp.NewServer("mcp-redis", version)
	server.SetInstructions("Redis MCP server. Inspect cache data, monitor connections, and analyze performance.")

	// redis_info
	server.AddTool(mcp.Tool{
		Name:        "redis_info",
		Description: "Get Redis server information and statistics",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"section": map[string]any{
					"type":        "string",
					"description": "Info section: server, clients, memory, persistence, stats, replication, cpu, cluster, keyspace, all (default: all)",
				},
			},
		},
	}, rs.handleInfo)

	// redis_keys
	server.AddTool(mcp.Tool{
		Name:        "redis_keys",
		Description: "Scan keys matching a pattern (uses SCAN, safe for production)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Key pattern (glob-style, e.g., 'user:*', 'session:*'). Default: '*'",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "Maximum keys to return (default: 100, max: 1000)",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "Filter by key type: string, list, set, zset, hash, stream",
				},
			},
		},
	}, rs.handleKeys)

	// redis_get
	server.AddTool(mcp.Tool{
		Name:        "redis_get",
		Description: "Get the value of a key (supports string, hash, list, set, zset)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Key name",
				},
				"start": map[string]any{
					"type":        "integer",
					"description": "Start index for list/zset (default: 0)",
				},
				"stop": map[string]any{
					"type":        "integer",
					"description": "Stop index for list/zset (default: -1, meaning all)",
				},
			},
			Required: []string{"key"},
		},
	}, rs.handleGet)

	// redis_ttl
	server.AddTool(mcp.Tool{
		Name:        "redis_ttl",
		Description: "Get the time-to-live for a key",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Key name",
				},
			},
			Required: []string{"key"},
		},
	}, rs.handleTTL)

	// redis_memory
	server.AddTool(mcp.Tool{
		Name:        "redis_memory",
		Description: "Get memory usage information for a key or the server",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Key name (optional, omit for server memory stats)",
				},
			},
		},
	}, rs.handleMemory)

	// redis_dbsize
	server.AddTool(mcp.Tool{
		Name:        "redis_dbsize",
		Description: "Get the number of keys in the current database",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, rs.handleDBSize)

	// redis_slowlog
	server.AddTool(mcp.Tool{
		Name:        "redis_slowlog",
		Description: "Get the slow query log",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"count": map[string]any{
					"type":        "integer",
					"description": "Number of entries to return (default: 10)",
				},
			},
		},
	}, rs.handleSlowLog)

	// redis_client_list
	server.AddTool(mcp.Tool{
		Name:        "redis_client_list",
		Description: "Get list of connected clients",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "Filter by client type: normal, master, replica, pubsub",
				},
			},
		},
	}, rs.handleClientList)

	// redis_pubsub_channels
	server.AddTool(mcp.Tool{
		Name:        "redis_pubsub_channels",
		Description: "Get active pub/sub channels",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Channel pattern (default: '*')",
				},
			},
		},
	}, rs.handlePubSubChannels)

	// redis_config_get
	server.AddTool(mcp.Tool{
		Name:        "redis_config_get",
		Description: "Get Redis configuration parameters",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Config parameter pattern (default: '*')",
				},
			},
		},
	}, rs.handleConfigGet)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (s *redisServer) handleInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	section := v.String("section", "all")

	var info string
	var err error

	if section == "all" {
		info, err = s.client.Info(ctx).Result()
	} else {
		info, err = s.client.Info(ctx, section).Result()
	}

	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse info into structured format
	result := parseRedisInfo(info)
	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"info": result,
	})
}

func parseRedisInfo(info string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	var currentSection string

	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			currentSection = strings.TrimPrefix(line, "# ")
			result[currentSection] = make(map[string]string)
			continue
		}

		if idx := strings.Index(line, ":"); idx > 0 && currentSection != "" {
			key := line[:idx]
			value := line[idx+1:]
			result[currentSection][key] = value
		}
	}

	return result
}

func (s *redisServer) handleKeys(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pattern := v.String("pattern", "*")
	count := v.IntRange("count", 100, 1, 1000)
	keyType := v.String("type", "")

	var keys []string
	var cursor uint64

	for len(keys) < count {
		var scanKeys []string
		var err error

		if keyType != "" {
			scanKeys, cursor, err = s.client.ScanType(ctx, cursor, pattern, int64(count-len(keys)), keyType).Result()
		} else {
			scanKeys, cursor, err = s.client.Scan(ctx, cursor, pattern, int64(count-len(keys))).Result()
		}

		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		keys = append(keys, scanKeys...)

		if cursor == 0 {
			break
		}
	}

	// Limit to requested count
	if len(keys) > count {
		keys = keys[:count]
	}

	// Get type info for each key
	keyInfos := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		kt, _ := s.client.Type(ctx, key).Result()
		ttl, _ := s.client.TTL(ctx, key).Result()

		info := map[string]any{
			"key":  key,
			"type": kt,
		}

		if ttl > 0 {
			info["ttl_seconds"] = int(ttl.Seconds())
		} else if ttl == -1 {
			info["ttl"] = "no expiry"
		} else if ttl == -2 {
			info["ttl"] = "key does not exist"
		}

		keyInfos = append(keyInfos, info)
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"pattern": pattern,
		"count":   len(keyInfos),
		"keys":    keyInfos,
	})
}

func (s *redisServer) handleGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	key := v.Required("key")
	start := v.Int("start", 0)
	stop := v.Int("stop", -1)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get key type
	keyType, err := s.client.Type(ctx, key).Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if keyType == "none" {
		return mcp.ErrorResult(fmt.Errorf("key does not exist")), nil
	}

	result := map[string]any{
		"ok":   true,
		"key":  key,
		"type": keyType,
	}

	switch keyType {
	case "string":
		val, err := s.client.Get(ctx, key).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		result["value"] = val

	case "list":
		vals, err := s.client.LRange(ctx, key, int64(start), int64(stop)).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		length, _ := s.client.LLen(ctx, key).Result()
		result["value"] = vals
		result["length"] = length

	case "set":
		vals, err := s.client.SMembers(ctx, key).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		result["value"] = vals
		result["cardinality"] = len(vals)

	case "zset":
		vals, err := s.client.ZRangeWithScores(ctx, key, int64(start), int64(stop)).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		members := make([]map[string]any, len(vals))
		for i, z := range vals {
			members[i] = map[string]any{
				"member": z.Member,
				"score":  z.Score,
			}
		}
		card, _ := s.client.ZCard(ctx, key).Result()
		result["value"] = members
		result["cardinality"] = card

	case "hash":
		vals, err := s.client.HGetAll(ctx, key).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		result["value"] = vals
		result["field_count"] = len(vals)

	case "stream":
		// Get stream info
		info, err := s.client.XInfoStream(ctx, key).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		result["length"] = info.Length
		result["first_entry"] = info.FirstEntry
		result["last_entry"] = info.LastEntry

	default:
		result["value"] = fmt.Sprintf("unsupported type: %s", keyType)
	}

	// Get TTL
	ttl, _ := s.client.TTL(ctx, key).Result()
	if ttl > 0 {
		result["ttl_seconds"] = int(ttl.Seconds())
	} else if ttl == -1 {
		result["ttl"] = "no expiry"
	}

	return mcp.JSONResult(result)
}

func (s *redisServer) handleTTL(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	key := v.Required("key")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ttl, err := s.client.TTL(ctx, key).Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	result := map[string]any{
		"ok":  true,
		"key": key,
	}

	if ttl > 0 {
		result["ttl_seconds"] = int(ttl.Seconds())
		result["ttl_human"] = ttl.String()
		result["expires_at"] = time.Now().Add(ttl).Format(time.RFC3339)
	} else if ttl == -1 {
		result["ttl"] = "no expiry"
	} else if ttl == -2 {
		result["ttl"] = "key does not exist"
	}

	return mcp.JSONResult(result)
}

func (s *redisServer) handleMemory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	key := v.String("key", "")

	if key != "" {
		// Get memory usage for specific key
		usage, err := s.client.MemoryUsage(ctx, key).Result()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(map[string]any{
			"ok":          true,
			"key":         key,
			"memory_bytes": usage,
			"memory_human": formatBytes(usage),
		})
	}

	// Get server memory stats
	info, err := s.client.Info(ctx, "memory").Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"info": parseRedisInfo(info),
	})
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (s *redisServer) handleDBSize(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	size, err := s.client.DBSize(ctx).Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"keys":  size,
	})
}

func (s *redisServer) handleSlowLog(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	count := v.Int("count", 10)

	logs, err := s.client.SlowLogGet(ctx, int64(count)).Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	entries := make([]map[string]any, len(logs))
	for i, log := range logs {
		entries[i] = map[string]any{
			"id":            log.ID,
			"time":          log.Time.Format(time.RFC3339),
			"duration_us":   log.Duration,
			"duration_ms":   float64(log.Duration) / 1000,
			"command":       strings.Join(log.Args, " "),
			"client_addr":   log.ClientAddr,
			"client_name":   log.ClientName,
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(entries),
		"entries": entries,
	})
}

func (s *redisServer) handleClientList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	clientType := v.String("type", "")

	var result string
	var err error

	if clientType != "" {
		result, err = s.client.ClientList(ctx).Result()
	} else {
		result, err = s.client.ClientList(ctx).Result()
	}

	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse client list
	clients := parseClientList(result)

	// Filter by type if specified
	if clientType != "" {
		filtered := make([]map[string]string, 0)
		for _, c := range clients {
			if strings.Contains(c["flags"], clientTypeFlag(clientType)) {
				filtered = append(filtered, c)
			}
		}
		clients = filtered
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(clients),
		"clients": clients,
	})
}

func clientTypeFlag(t string) string {
	switch t {
	case "normal":
		return "N"
	case "master":
		return "M"
	case "replica", "slave":
		return "S"
	case "pubsub":
		return "P"
	default:
		return ""
	}
}

func parseClientList(list string) []map[string]string {
	var clients []map[string]string

	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		client := make(map[string]string)
		for _, pair := range strings.Split(line, " ") {
			if idx := strings.Index(pair, "="); idx > 0 {
				key := pair[:idx]
				value := pair[idx+1:]
				client[key] = value
			}
		}

		if len(client) > 0 {
			clients = append(clients, client)
		}
	}

	return clients
}

func (s *redisServer) handlePubSubChannels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pattern := v.String("pattern", "*")

	channels, err := s.client.PubSubChannels(ctx, pattern).Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get subscriber counts for each channel
	channelInfo := make([]map[string]any, len(channels))
	for i, ch := range channels {
		numsub, _ := s.client.PubSubNumSub(ctx, ch).Result()
		channelInfo[i] = map[string]any{
			"channel":     ch,
			"subscribers": numsub[ch],
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(channelInfo),
		"channels": channelInfo,
	})
}

func (s *redisServer) handleConfigGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pattern := v.String("pattern", "*")

	config, err := s.client.ConfigGet(ctx, pattern).Result()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"config": config,
	})
}
