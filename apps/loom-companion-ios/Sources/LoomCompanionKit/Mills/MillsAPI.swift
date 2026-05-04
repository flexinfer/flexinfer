// Mills API client (Phase 7 slice 7.5).
//
// Thin wrapper over LoomAPIClientProtocol that exposes the two read
// endpoints the mobile Mills screen consumes: pipeline runs and the
// latest KPI snapshot. Both are proxied by the HUD's /api/mills/* tier
// (see internal/hud/domain/mills/mills.go); this file does NOT talk to
// the operator directly. When the operator is unconfigured the proxy
// returns 503 — the screen surfaces that as a "Mills disabled" empty
// state, so all errors thrown here just bubble up via the standard
// LoomAPIError.

import Foundation

/// Mirrors `pkg/mills/store.PipelineRun` (default Go JSON encoding uses
/// uppercase field names, which is why CodingKeys do, too).
public struct MillsPipelineRun: Codable, Sendable, Identifiable, Hashable {
    public let id: String
    public let backlogID: String
    public let template: String
    public let state: String
    public let attempts: Int
    public let startedAt: Date?
    public let endedAt: Date?
    public let parentRunID: String?
    public let depth: Int?

    enum CodingKeys: String, CodingKey {
        case id = "ID"
        case backlogID = "BacklogID"
        case template = "Template"
        case state = "State"
        case attempts = "Attempts"
        case startedAt = "StartedAt"
        case endedAt = "EndedAt"
        case parentRunID = "ParentRunID"
        case depth = "Depth"
    }

    public init(
        id: String,
        backlogID: String,
        template: String,
        state: String,
        attempts: Int,
        startedAt: Date? = nil,
        endedAt: Date? = nil,
        parentRunID: String? = nil,
        depth: Int? = nil
    ) {
        self.id = id
        self.backlogID = backlogID
        self.template = template
        self.state = state
        self.attempts = attempts
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.parentRunID = parentRunID
        self.depth = depth
    }
}

/// Mirrors `pkg/mills/store.KPISnapshot`. `metrics` is an open map so
/// the screen can pluck whatever the operator decides to publish without
/// the Swift side needing a release for every metric addition.
public struct MillsKPISnapshot: Codable, Sendable, Hashable {
    public let id: Int64?
    public let snapshotAt: Date?
    public let windowSeconds: Int?
    public let metrics: [String: Double]

    enum CodingKeys: String, CodingKey {
        case id = "ID"
        case snapshotAt = "SnapshotAt"
        case windowSeconds = "WindowSeconds"
        case metrics = "Metrics"
    }

    public init(
        id: Int64? = nil,
        snapshotAt: Date? = nil,
        windowSeconds: Int? = nil,
        metrics: [String: Double] = [:]
    ) {
        self.id = id
        self.snapshotAt = snapshotAt
        self.windowSeconds = windowSeconds
        self.metrics = metrics
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try c.decodeIfPresent(Int64.self, forKey: .id)
        self.snapshotAt = try c.decodeIfPresent(Date.self, forKey: .snapshotAt)
        self.windowSeconds = try c.decodeIfPresent(Int.self, forKey: .windowSeconds)
        // The Go side serializes `Metrics` as map[string]any. Coerce
        // to [String: Double] best-effort; non-numeric values are dropped
        // since the screen only renders numbers.
        let raw = try c.decodeIfPresent([String: AnyCodable].self, forKey: .metrics) ?? [:]
        var coerced: [String: Double] = [:]
        for (k, v) in raw {
            if let d = v.doubleValue { coerced[k] = d }
        }
        self.metrics = coerced
    }
}

/// Read-only Mills client surface. ViewModels and previews depend on the
/// protocol so test fakes can short-circuit network calls.
public protocol MillsAPIProtocol: Sendable {
    func pipelineRuns() async throws -> [MillsPipelineRun]
    func latestKPI(window: String) async throws -> MillsKPISnapshot?
}

/// Concrete client backed by the existing `LoomAPIClientProtocol`. The
/// HUD's mills proxy returns 404 when no KPI snapshot exists yet — that
/// surfaces as `LoomAPIError.notFound` from the underlying client; the
/// Mills screen treats that as "no data yet" rather than an error.
public struct MillsAPI: MillsAPIProtocol, Sendable {
    private let client: LoomAPIClientProtocol

    public init(client: LoomAPIClientProtocol) {
        self.client = client
    }

    public func pipelineRuns() async throws -> [MillsPipelineRun] {
        try await client.request(.millsPipelineRuns)
    }

    public func latestKPI(window: String = "1d") async throws -> MillsKPISnapshot? {
        do {
            let snap: MillsKPISnapshot = try await client.request(.millsKPIs(window: window))
            return snap
        } catch let LoomAPIError.apiError(code, _, _) where code == .notFound || code == .notConfigured {
            // No snapshot yet, or LOOM_MILLS_OPERATOR_URL unset → render
            // empty state instead of an error.
            return nil
        }
    }
}
