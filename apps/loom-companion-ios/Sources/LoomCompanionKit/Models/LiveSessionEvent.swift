// LiveSessionEvent.swift — iOS-side mirror of the daemon EventBus payloads
// emitted by Phase 2.x of the spectator plan
// (`.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`).
//
// The Go side defines these in:
//   - internal/daemon/events.go (constants)
//   - pkg/agentcontext/publisher.go (SessionStartEvent / SessionEndEvent / AgentStatusChangeEvent)
//   - internal/hud/bridge/spawn_telemetry.go (ToolCallStartEvent / ToolCallEndEvent)
//
// We decode permissively (every field optional except the type) so a future
// payload addition on the Go side does not crash the iOS client.

import Foundation

/// Canonical event types the spectator UI consumes. Mirrors
/// `internal/daemon.EventType` constants — keep in sync.
public enum LiveSessionEventType: String, Sendable, Codable {
    case sessionStart = "session.start"
    case sessionEnd = "session.end"
    case agentStatusChange = "agent.status.change"
    case toolCallStart = "tool.call.start"
    case toolCallEnd = "tool.call.end"
}

/// Subset of the daemon's SSE envelope we care about for the spectator UI.
public struct LiveSessionEventEnvelope: Sendable, Decodable {
    public let id: String?
    public let type: String
    public let timestamp: String?
    public let data: LiveSessionEventData

    public var canonicalType: LiveSessionEventType? {
        LiveSessionEventType(rawValue: type)
    }
}

/// Union of all five payload shapes. Fields not relevant to a given event
/// type are nil; consumers branch on `LiveSessionEventEnvelope.canonicalType`.
public struct LiveSessionEventData: Sendable, Decodable {
    public let sessionID: String?
    public let agentID: String?
    public let status: String?
    public let callID: String?
    public let toolName: String?
    public let serverName: String?
    public let argsRedacted: [String: AnyCodable]?
    public let argsTier: String?
    public let durationMs: Int?
    public let exitCode: Int?
    public let resultSummary: String?
    public let error: String?
    public let startedAt: String?
    public let endedAt: String?

    private enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case agentID = "agent_id"
        case status
        case callID = "call_id"
        case toolName = "tool_name"
        case serverName = "server_name"
        case argsRedacted = "args_redacted"
        case argsTier = "args_tier"
        case durationMs = "duration_ms"
        case exitCode = "exit_code"
        case resultSummary = "result_summary"
        case error
        case startedAt = "started_at"
        case endedAt = "ended_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = try c.decodeIfPresent(String.self, forKey: .sessionID)
        agentID = try c.decodeIfPresent(String.self, forKey: .agentID)
        status = try c.decodeIfPresent(String.self, forKey: .status)
        callID = try c.decodeIfPresent(String.self, forKey: .callID)
        toolName = try c.decodeIfPresent(String.self, forKey: .toolName)
        serverName = try c.decodeIfPresent(String.self, forKey: .serverName)
        argsRedacted = try c.decodeIfPresent([String: AnyCodable].self, forKey: .argsRedacted)
        argsTier = try c.decodeIfPresent(String.self, forKey: .argsTier)
        durationMs = try c.decodeIfPresent(Int.self, forKey: .durationMs)
        exitCode = try c.decodeIfPresent(Int.self, forKey: .exitCode)
        resultSummary = try c.decodeIfPresent(String.self, forKey: .resultSummary)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
    }
}

/// Status-dot rendering value with a stable Sendable type for the view layer.
public enum LiveAgentStatus: String, Sendable {
    case active
    case idle
    case offline
    case expired
    case unknown

    public init(raw: String?) {
        switch raw {
        case "active": self = .active
        case "idle": self = .idle
        case "offline": self = .offline
        case "expired": self = .expired
        default: self = .unknown
        }
    }
}

/// One tool call entry inside a session's ring buffer. Mirrors the
/// `ToolCall` interface in the HUD `liveSessions.svelte.ts` store.
public struct LiveToolCall: Sendable, Identifiable, Equatable {
    public let id: String
    public var toolName: String
    public var serverName: String?
    public var durationMs: Int?
    public var exitCode: Int?
    public var resultSummary: String?
    public var error: String?
    public var status: String?
    public var startedAt: String?
    public var endedAt: String?
    public var inFlight: Bool

    public init(
        id: String,
        toolName: String,
        serverName: String? = nil,
        durationMs: Int? = nil,
        exitCode: Int? = nil,
        resultSummary: String? = nil,
        error: String? = nil,
        status: String? = nil,
        startedAt: String? = nil,
        endedAt: String? = nil,
        inFlight: Bool = false
    ) {
        self.id = id
        self.toolName = toolName
        self.serverName = serverName
        self.durationMs = durationMs
        self.exitCode = exitCode
        self.resultSummary = resultSummary
        self.error = error
        self.status = status
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.inFlight = inFlight
    }

    /// `server.tool` for MCP-routed tools; just `tool` for builtins.
    public var displayName: String {
        if let server = serverName, !server.isEmpty { return "\(server).\(toolName)" }
        return toolName
    }
}

/// One spectator-visible session. Mirrors the HUD store's `LiveSession`.
public struct LiveSession: Sendable, Identifiable, Equatable {
    public let id: String
    public var agentID: String
    public var agentStatus: LiveAgentStatus
    public var recentCalls: [LiveToolCall]
    public var firstSeen: Date
    public var lastActivity: Date
    public var endedAt: Date?

    public init(
        id: String,
        agentID: String,
        agentStatus: LiveAgentStatus = .unknown,
        recentCalls: [LiveToolCall] = [],
        firstSeen: Date = Date(),
        lastActivity: Date = Date(),
        endedAt: Date? = nil
    ) {
        self.id = id
        self.agentID = agentID
        self.agentStatus = agentStatus
        self.recentCalls = recentCalls
        self.firstSeen = firstSeen
        self.lastActivity = lastActivity
        self.endedAt = endedAt
    }
}
