import Foundation

// Mirrors internal/hud/bridge/spawn_telemetry.go (Go canonical model) so the
// iOS companion can decode /api/mobile/v1/agent/spawn/{id}/telemetry payloads
// and SSE deltas. Keep field names and JSON keys in sync with the Go struct
// tags; new fields must be added to both sides.

/// SDK-sourced structured telemetry for a single headless agent spawn.
public struct SpawnTelemetry: Codable, Equatable, Hashable, Sendable {
    /// External session identifier (claude session_id or codex thread_id).
    public let externalSessionID: String?
    /// Number of agent turns observed so far.
    public let turnCount: Int
    /// Aggregate cost across all turns, in USD.
    public let totalCostUSD: Double
    /// Aggregate token usage across all turns.
    public let tokenUsage: SpawnTokenUsage
    /// Optional per-model cost/token breakdown keyed by model id.
    public let modelUsage: [String: SpawnModelUse]?
    /// Tool calls observed during the spawn (capped server-side).
    public let toolCalls: [SpawnToolCall]?
    /// File modifications attributed to the agent (capped server-side).
    public let fileChanges: [SpawnFileChange]?
    /// Errors raised by the agent runtime.
    public let errors: [SpawnAgentError]?
    /// Final stop reason ("end_turn", "max_turns", "max_budget", ...).
    public let stopReason: String?
    /// Final assistant message text (when terminated cleanly).
    public let lastMessage: String?

    public init(
        externalSessionID: String? = nil,
        turnCount: Int = 0,
        totalCostUSD: Double = 0,
        tokenUsage: SpawnTokenUsage = SpawnTokenUsage(),
        modelUsage: [String: SpawnModelUse]? = nil,
        toolCalls: [SpawnToolCall]? = nil,
        fileChanges: [SpawnFileChange]? = nil,
        errors: [SpawnAgentError]? = nil,
        stopReason: String? = nil,
        lastMessage: String? = nil
    ) {
        self.externalSessionID = externalSessionID
        self.turnCount = turnCount
        self.totalCostUSD = totalCostUSD
        self.tokenUsage = tokenUsage
        self.modelUsage = modelUsage
        self.toolCalls = toolCalls
        self.fileChanges = fileChanges
        self.errors = errors
        self.stopReason = stopReason
        self.lastMessage = lastMessage
    }

    enum CodingKeys: String, CodingKey {
        case externalSessionID = "external_session_id"
        case turnCount = "turn_count"
        case totalCostUSD = "total_cost_usd"
        case tokenUsage = "token_usage"
        case modelUsage = "model_usage"
        case toolCalls = "tool_calls"
        case fileChanges = "file_changes"
        case errors
        case stopReason = "stop_reason"
        case lastMessage = "last_message"
    }
}

/// Aggregate token usage across all turns of a spawn.
public struct SpawnTokenUsage: Codable, Equatable, Hashable, Sendable {
    public let inputTokens: Int
    public let outputTokens: Int
    public let cacheCreationTokens: Int
    public let cacheReadTokens: Int

    public init(
        inputTokens: Int = 0,
        outputTokens: Int = 0,
        cacheCreationTokens: Int = 0,
        cacheReadTokens: Int = 0
    ) {
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheCreationTokens = cacheCreationTokens
        self.cacheReadTokens = cacheReadTokens
    }

    enum CodingKeys: String, CodingKey {
        case inputTokens = "input_tokens"
        case outputTokens = "output_tokens"
        case cacheCreationTokens = "cache_creation_tokens"
        case cacheReadTokens = "cache_read_tokens"
    }
}

/// Per-model cost and token usage breakdown (mirrors Go `bridge.ModelUse`).
public struct SpawnModelUse: Codable, Equatable, Hashable, Sendable {
    public let costUSD: Double
    public let inputTokens: Int
    public let outputTokens: Int

    public init(costUSD: Double = 0, inputTokens: Int = 0, outputTokens: Int = 0) {
        self.costUSD = costUSD
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
    }

    enum CodingKeys: String, CodingKey {
        case costUSD = "cost_usd"
        case inputTokens = "input_tokens"
        case outputTokens = "output_tokens"
    }
}

/// A single tool invocation captured during a spawn (mirrors Go `bridge.ToolCallEntry`).
public struct SpawnToolCall: Codable, Equatable, Hashable, Sendable, Identifiable {
    public let name: String
    public let serverName: String?
    public let durationMs: Int?
    /// Pointer-style exit code: present (including `0`) when the tool reported
    /// one, `nil` when it did not. Mirrors Go `*int`.
    public let exitCode: Int?
    public let error: String?
    public let timestamp: String

    /// Synthesized identifier for SwiftUI list rendering. Combines name +
    /// timestamp + a positional hash so duplicate names within a spawn don't
    /// collapse.
    public var id: String { "\(timestamp)|\(name)|\(durationMs ?? -1)" }

    public init(
        name: String,
        serverName: String? = nil,
        durationMs: Int? = nil,
        exitCode: Int? = nil,
        error: String? = nil,
        timestamp: String
    ) {
        self.name = name
        self.serverName = serverName
        self.durationMs = durationMs
        self.exitCode = exitCode
        self.error = error
        self.timestamp = timestamp
    }

    enum CodingKeys: String, CodingKey {
        case name
        case serverName = "server_name"
        case durationMs = "duration_ms"
        case exitCode = "exit_code"
        case error
        case timestamp
    }
}

/// A file modification attributed to the agent (mirrors Go `bridge.FileChangeEntry`).
public struct SpawnFileChange: Codable, Equatable, Hashable, Sendable, Identifiable {
    public let path: String
    /// One of `create`, `modify`, `delete`.
    public let kind: String

    public var id: String { "\(kind)|\(path)" }

    public init(path: String, kind: String) {
        self.path = path
        self.kind = kind
    }
}

/// An error raised by the agent runtime (mirrors Go `bridge.AgentError`).
public struct SpawnAgentError: Codable, Equatable, Hashable, Sendable, Identifiable {
    /// One of `max_turns`, `max_budget`, `rate_limit`, `execution`, `tool_failure`.
    public let type: String
    public let message: String
    public let time: String

    public var id: String { "\(time)|\(type)" }

    public init(type: String, message: String, time: String) {
        self.type = type
        self.message = message
        self.time = time
    }
}
