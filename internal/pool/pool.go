// Package pool provides connection pooling for MCP servers.
package pool

import (
	kitpool "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/pool"
)

type Conn = kitpool.Conn
type Stats = kitpool.Stats
type Pool = kitpool.Pool
type DialFunc = kitpool.DialFunc
type Config = kitpool.Config

func New(cfg Config) *Pool {
	return kitpool.New(cfg)
}