package daemon

import (
	"context"
	gosync "sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/router"
)

const (
	defaultDaemonControlRPCTimeout = 30 * time.Second
	defaultDaemonToolRPCTimeout    = 60 * time.Second
	maxDaemonToolRPCTimeout        = 15 * time.Minute
	autoDeriveDaemonTimeoutBuffer  = 60 * time.Second
)

// Pipeline stage constants for audit traceability.
const (
	stageParse      = "parse"
	stageAuth       = "authorize"
	stagePolicy     = "policy"
	stageCache      = "cache"
	stageGate       = "gate"
	stageRoute      = "route"
	stageBuild      = "build"
	stageExecute    = "execute"
	stageOutputScan = "output_scan"
)

// callPipeline executes the daemon tool-call flow in ordered stages.
type callPipeline struct {
	daemon *Daemon
	ctx    context.Context
	msg    *mcp.Message

	params     callParams
	serverName string
	toolName   string
	method     string
	cacheKey   string
	auditStart time.Time
	stage      string

	conn      *pool.Conn
	target    router.Target
	targetStr string
	callMu    *gosync.Mutex
	lockHeld  bool

	routingPreference       RoutingPreference
	preferHubRetryEligible  bool
	localRetryUsed          bool
	localTransportRetryUsed bool
}

func newCallPipeline(d *Daemon, ctx context.Context, msg *mcp.Message) *callPipeline {
	return &callPipeline{
		daemon: d,
		ctx:    ctx,
		msg:    msg,
	}
}

// startStageSpan begins a tracing span for the named pipeline stage.
// It safely handles a nil p.ctx (which occurs in unit tests that construct
// a callPipeline directly) and updates p.ctx so downstream stages nest.
func (p *callPipeline) startStageSpan(name string) trace.Span {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var span trace.Span
	p.ctx, span = p.daemon.daemonTracer().Start(ctx, name)
	return span
}
