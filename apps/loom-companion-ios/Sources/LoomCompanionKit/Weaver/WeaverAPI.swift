// Weaver API client (S7b of weaver-qwen3 plan).
//
// Read-only mobile surface for the HUD's /api/weaver/* and
// /api/aimodels/roles endpoints. Mirrors what `internal/hud/domain/
// weaver/handlers.go` and `internal/hud/domain/aimodels/handlers.go`
// return. Used by the iOS WeaverScreen.
//
// See services/loom-core/.loom/111-product-spec-weaver-qwen3-
// integration-2026-05-08.md (IOS-001..IOS-002).

import Foundation

/// Mirrors the daemon's loom/weaver/status response (extended in S4
/// with preflight fields). Permissive on unknown keys via the
/// `[String: AnyCodable]` overflow bucket pattern used elsewhere.
public struct WeaverStatus: Codable, Sendable, Hashable {
    public let enabled: Bool
    public let routerModel: String?
    public let subagentModel: String?
    public let domains: [WeaverDomainSummary]?

    // Preflight (S4)
    public let degraded: Bool?
    public let missingModels: [String]?
    public let readyModels: [String]?
    public let catalogSize: Int?
    public let catalogError: String?
    public let preflightAt: String?

    enum CodingKeys: String, CodingKey {
        case enabled
        case routerModel = "router_model"
        case subagentModel = "subagent_model"
        case domains
        case degraded
        case missingModels = "missing_models"
        case readyModels = "ready_models"
        case catalogSize = "catalog_size"
        case catalogError = "catalog_error"
        case preflightAt = "preflight_at"
    }

    public init(
        enabled: Bool,
        routerModel: String? = nil,
        subagentModel: String? = nil,
        domains: [WeaverDomainSummary]? = nil,
        degraded: Bool? = nil,
        missingModels: [String]? = nil,
        readyModels: [String]? = nil,
        catalogSize: Int? = nil,
        catalogError: String? = nil,
        preflightAt: String? = nil
    ) {
        self.enabled = enabled
        self.routerModel = routerModel
        self.subagentModel = subagentModel
        self.domains = domains
        self.degraded = degraded
        self.missingModels = missingModels
        self.readyModels = readyModels
        self.catalogSize = catalogSize
        self.catalogError = catalogError
        self.preflightAt = preflightAt
    }

    /// True when the daemon has run preflight and at least one
    /// configured model is absent from the FlexInfer catalog.
    public var isDegraded: Bool { degraded == true }
}

public struct WeaverDomainSummary: Codable, Sendable, Hashable, Identifiable {
    public let name: String
    public let description: String?
    public let model: String?
    public let backend: String?
    public let tools: [String]?

    public var id: String { name }

    public init(
        name: String,
        description: String? = nil,
        model: String? = nil,
        backend: String? = nil,
        tools: [String]? = nil
    ) {
        self.name = name
        self.description = description
        self.model = model
        self.backend = backend
        self.tools = tools
    }
}

/// Mirrors `pkg/weaver.QueryHistoryEntry` (rendered shape from
/// `loom/weaver/history`).
public struct WeaverHistoryEntry: Codable, Sendable, Hashable, Identifiable {
    public let queryId: String
    public let query: String?
    public let status: String?
    public let latencyMs: Int64?
    public let totalTokens: Int?
    public let domainsUsed: [String]?
    public let parentSessionId: String?
    public let timestamp: String?

    public var id: String { queryId }

    enum CodingKeys: String, CodingKey {
        case queryId = "query_id"
        case query
        case status
        case latencyMs = "latency_ms"
        case totalTokens = "total_tokens"
        case domainsUsed = "domains_used"
        case parentSessionId = "parent_session_id"
        case timestamp
    }

    public init(
        queryId: String,
        query: String? = nil,
        status: String? = nil,
        latencyMs: Int64? = nil,
        totalTokens: Int? = nil,
        domainsUsed: [String]? = nil,
        parentSessionId: String? = nil,
        timestamp: String? = nil
    ) {
        self.queryId = queryId
        self.query = query
        self.status = status
        self.latencyMs = latencyMs
        self.totalTokens = totalTokens
        self.domainsUsed = domainsUsed
        self.parentSessionId = parentSessionId
        self.timestamp = timestamp
    }
}

public struct WeaverHistoryResponse: Codable, Sendable, Hashable {
    public let entries: [WeaverHistoryEntry]?

    public init(entries: [WeaverHistoryEntry]? = nil) {
        self.entries = entries
    }
}

public struct WeaverMetrics: Codable, Sendable, Hashable {
    public let totalQueries: Int
    public let avgLatencyMs: Double
    public let errorRate: Double
    public let totalTokens: Int
    public let errorCount: Int

    enum CodingKeys: String, CodingKey {
        case totalQueries = "total_queries"
        case avgLatencyMs = "avg_latency_ms"
        case errorRate = "error_rate"
        case totalTokens = "total_tokens"
        case errorCount = "error_count"
    }

    public init(
        totalQueries: Int = 0,
        avgLatencyMs: Double = 0,
        errorRate: Double = 0,
        totalTokens: Int = 0,
        errorCount: Int = 0
    ) {
        self.totalQueries = totalQueries
        self.avgLatencyMs = avgLatencyMs
        self.errorRate = errorRate
        self.totalTokens = totalTokens
        self.errorCount = errorCount
    }
}

/// Mirrors GET /api/aimodels/roles (S6).
public struct AIModelRoleEntry: Codable, Sendable, Hashable, Identifiable {
    public let role: String
    public let primary: String
    public let fallbacks: [String]?

    public var id: String { role }

    public init(role: String, primary: String, fallbacks: [String]? = nil) {
        self.role = role
        self.primary = primary
        self.fallbacks = fallbacks
    }
}

public struct AIModelRolesResponse: Codable, Sendable, Hashable {
    public let roles: [AIModelRoleEntry]
    public let overridePath: String?

    enum CodingKeys: String, CodingKey {
        case roles
        case overridePath = "override_path"
    }

    public init(roles: [AIModelRoleEntry] = [], overridePath: String? = nil) {
        self.roles = roles
        self.overridePath = overridePath
    }
}

/// Read-only Weaver client surface. ViewModels and previews depend on
/// the protocol so test fakes can short-circuit network calls.
public protocol WeaverAPIProtocol: Sendable {
    func status() async throws -> WeaverStatus
    func history() async throws -> WeaverHistoryResponse
    func metrics() async throws -> WeaverMetrics
    func roles() async throws -> AIModelRolesResponse
}

/// Concrete client backed by the existing `LoomAPIClientProtocol`.
public struct WeaverAPI: WeaverAPIProtocol, Sendable {
    private let client: LoomAPIClientProtocol

    public init(client: LoomAPIClientProtocol) {
        self.client = client
    }

    public func status() async throws -> WeaverStatus {
        try await client.request(.weaverStatus)
    }

    public func history() async throws -> WeaverHistoryResponse {
        do {
            return try await client.request(.weaverHistory)
        } catch let LoomAPIError.apiError(code, _, _)
            where code == .notFound || code == .notConfigured
        {
            return WeaverHistoryResponse(entries: [])
        }
    }

    public func metrics() async throws -> WeaverMetrics {
        do {
            return try await client.request(.weaverMetrics)
        } catch let LoomAPIError.apiError(code, _, _)
            where code == .notFound || code == .notConfigured
        {
            return WeaverMetrics()
        }
    }

    public func roles() async throws -> AIModelRolesResponse {
        do {
            return try await client.request(.aimodelsRoles)
        } catch let LoomAPIError.apiError(code, _, _)
            where code == .notFound || code == .notConfigured
        {
            return AIModelRolesResponse()
        }
    }
}
