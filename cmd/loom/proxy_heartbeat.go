// proxy_heartbeat.go — HUD heartbeat, agent identity resolution, and namespace hashing.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud"
)

// proxyHeartbeat fires an async heartbeat to the HUD for proxy-level agent identification.
// This provides universal heartbeat coverage for any agent using loom proxy.
func proxyHeartbeat(agentType string) {
	resolvedAgentID, resolvedAgentType := resolveProxyIdentity(agentType)
	proxyNamespaceOnce.Do(func() {
		proxyNamespace = inferGitNamespace()
	})
	bodyMap := map[string]any{
		"agent_id":       resolvedAgentID,
		"status":         "active",
		"agent_type":     resolvedAgentType,
		"ensure_session": true,
	}
	if strings.TrimSpace(proxyNamespace) != "" {
		bodyMap["namespace"] = proxyNamespace
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Try port file first, fall back to default.
	port := "3333"
	if data, err := os.ReadFile(hud.PortFilePath()); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			port = p
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://127.0.0.1:"+port+"/api/agent/heartbeat",
		strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func resolveProxyIdentity(agentHint string) (agentID, agentType string) {
	agentType = strings.TrimSpace(agentHint)
	if agentType == "" {
		agentType = "proxy"
	}

	proxyIdentityOnce.Do(func() {
		if override := strings.TrimSpace(os.Getenv("LOOM_PROXY_AGENT_ID")); override != "" {
			proxyAgentID = override
			return
		}

		typePart := sanitizeIDPart(agentType)
		if typePart == "" {
			typePart = "proxy"
		}

		host, err := os.Hostname()
		if err != nil {
			host = "host"
		}
		hostPart := sanitizeIDPart(host)
		if hostPart == "" {
			hostPart = "host"
		}

		pidPart := strconv.Itoa(os.Getpid())
		nsHash := namespaceDigest(inferGitNamespace())
		if nsHash != "" {
			proxyAgentID = fmt.Sprintf("%s-%s-%s-%s", typePart, hostPart, pidPart, nsHash)
			return
		}
		proxyAgentID = fmt.Sprintf("%s-%s-%s", typePart, hostPart, pidPart)
	})

	return proxyAgentID, agentType
}

func sanitizeIDPart(input string) string {
	if input == "" {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	b.Grow(len(normalized))
	prevDash := false
	for _, r := range normalized {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func namespaceDigest(namespace string) string {
	if strings.TrimSpace(namespace) == "" {
		return ""
	}
	sum := sha1.Sum([]byte(namespace))
	return hex.EncodeToString(sum[:])[:8]
}
