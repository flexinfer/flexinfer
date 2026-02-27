package router

import (
	kitrouter "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/router"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

type Target = kitrouter.Target

const (
	TargetLocal       = kitrouter.TargetLocal
	TargetHub         = kitrouter.TargetHub
	TargetUnavailable = kitrouter.TargetUnavailable
)

type RouteDecision = kitrouter.RouteDecision
type Health = kitrouter.Health
type Router = kitrouter.Router
type Config = kitrouter.Config
type Proxy = kitrouter.Proxy
type HubClient = kitrouter.HubClient

func New(cfg Config) *Router {
	return kitrouter.New(cfg)
}

func NewProxy(r *Router, transport mcp.Transport, server string, target Target) *Proxy {
	return kitrouter.NewProxy(r, transport, server, target)
}

func NewHubClient(url, token string) *HubClient {
	return kitrouter.NewHubClient(url, token)
}

func NewHubClientWithCFAccess(url, token, cfAccessClientID, cfAccessClientSecret string) *HubClient {
	return kitrouter.NewHubClientWithCFAccess(url, token, cfAccessClientID, cfAccessClientSecret)
}
