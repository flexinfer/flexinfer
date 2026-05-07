// LiveSessionsViewModel.swift — Phase 5 of the spectator plan
// (`.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`).
//
// iOS-side mirror of the HUD `liveSessionsStore` (Phase 3). Subscribes to
// the existing `SSEEventBroadcaster` shared with the rest of the
// LoomCompanion app and maintains a session map keyed by session_id.
//
// Decoding is permissive: malformed events are skipped silently rather
// than tearing down the stream, so a future Go-side payload addition does
// not crash the iOS client. Tool-args on the wire are already redacted at
// TierPublic by the producers (Phase 1.3 / 2.x); we display verbatim.

import Foundation

/// Maximum tool calls retained per session. Matches the HUD store cap.
public let liveSessionsRecentCallsLimit = 20

/// Sessions retain visibility this long after `session.end` so the final
/// tail is visible before reaping. Matches the HUD store retention.
public let liveSessionsEndedRetentionSeconds: TimeInterval = 30

@Observable
@MainActor
public final class LiveSessionsViewModel {
    /// Sessions keyed by session_id. Sorted views derive from this dict.
    public private(set) var sessionsByID: [String: LiveSession] = [:]

    /// Monotonic event counter — used by tests and last-update indicators.
    public private(set) var eventCount: Int = 0

    @ObservationIgnored
    private var registrationID: UUID?

    @ObservationIgnored
    private weak var broadcaster: SSEEventBroadcaster?

    @ObservationIgnored
    private var reapTask: Task<Void, Never>?

    public init() {}

    /// Subscribe to the broadcaster's event stream. Idempotent — calling
    /// twice is a no-op so multiple `.task` modifiers in the view tree
    /// don't double-register handlers.
    public func subscribe(to broadcaster: SSEEventBroadcaster) {
        if registrationID != nil { return }
        self.broadcaster = broadcaster
        let id = broadcaster.register { [weak self] event in
            await self?.handle(event)
        }
        registrationID = id
        startReapTask()
    }

    public func unsubscribe() {
        if let id = registrationID, let broadcaster {
            broadcaster.unregister(id)
        }
        registrationID = nil
        broadcaster = nil
        reapTask?.cancel()
        reapTask = nil
    }

    /// Reset state. Used by tests and on auth changes.
    public func reset() {
        sessionsByID = [:]
        eventCount = 0
    }

    /// Visible sessions, most-recent activity first, with reaped entries
    /// excluded.
    public var visibleSessions: [LiveSession] {
        sessionsByID.values
            .filter { session in
                guard let endedAt = session.endedAt else { return true }
                return Date().timeIntervalSince(endedAt) < liveSessionsEndedRetentionSeconds
            }
            .sorted { $0.lastActivity > $1.lastActivity }
    }

    public var activeSessionCount: Int {
        visibleSessions.filter { $0.endedAt == nil }.count
    }

    public var inFlightCallCount: Int {
        visibleSessions.reduce(0) { acc, s in
            acc + s.recentCalls.filter { $0.inFlight }.count
        }
    }

    /// Decode an SSE event and apply it. Public for tests; the broadcaster
    /// hookup calls this internally in production.
    public func handle(_ event: SSEEvent) {
        eventCount += 1
        guard let envelope = decode(event) else { return }
        switch envelope.canonicalType {
        case .sessionStart:
            applySessionStart(envelope.data)
        case .sessionEnd:
            applySessionEnd(envelope.data)
        case .agentStatusChange:
            applyAgentStatusChange(envelope.data)
        case .toolCallStart:
            applyToolCallStart(envelope.data)
        case .toolCallEnd:
            applyToolCallEnd(envelope.data)
        case .none:
            // Not a spectator event — the broadcaster gives us the full
            // SSE stream including non-spectator types.
            return
        }
    }

    // MARK: - Internal handlers

    private func applySessionStart(_ data: LiveSessionEventData) {
        guard let sid = data.sessionID, !sid.isEmpty else { return }
        var session = sessionsByID[sid]
            ?? LiveSession(id: sid, agentID: data.agentID ?? "")
        if (session.agentID.isEmpty), let aid = data.agentID, !aid.isEmpty {
            session.agentID = aid
        }
        session.lastActivity = Date()
        session.endedAt = nil
        sessionsByID[sid] = session
    }

    private func applySessionEnd(_ data: LiveSessionEventData) {
        guard let sid = data.sessionID, !sid.isEmpty else { return }
        guard var session = sessionsByID[sid] else { return }
        session.endedAt = Date()
        session.lastActivity = Date()
        sessionsByID[sid] = session
    }

    private func applyAgentStatusChange(_ data: LiveSessionEventData) {
        guard let aid = data.agentID, !aid.isEmpty else { return }
        let status = LiveAgentStatus(raw: data.status)
        for (sid, session) in sessionsByID where session.agentID == aid {
            var copy = session
            copy.agentStatus = status
            copy.lastActivity = Date()
            sessionsByID[sid] = copy
        }
    }

    private func applyToolCallStart(_ data: LiveSessionEventData) {
        guard let sid = data.sessionID, let cid = data.callID,
              !sid.isEmpty, !cid.isEmpty,
              let toolName = data.toolName, !toolName.isEmpty else {
            return
        }
        var session = sessionsByID[sid]
            ?? LiveSession(id: sid, agentID: data.agentID ?? "")
        if session.agentID.isEmpty, let aid = data.agentID, !aid.isEmpty {
            session.agentID = aid
        }
        let call = LiveToolCall(
            id: cid,
            toolName: toolName,
            serverName: data.serverName?.nilIfEmpty,
            startedAt: data.startedAt,
            inFlight: true
        )
        session.recentCalls.insert(call, at: 0)
        if session.recentCalls.count > liveSessionsRecentCallsLimit {
            session.recentCalls.removeLast(session.recentCalls.count - liveSessionsRecentCallsLimit)
        }
        session.lastActivity = Date()
        sessionsByID[sid] = session
    }

    private func applyToolCallEnd(_ data: LiveSessionEventData) {
        guard let sid = data.sessionID, !sid.isEmpty else { return }
        guard var session = sessionsByID[sid] else { return }

        if let cid = data.callID,
           let idx = session.recentCalls.firstIndex(where: { $0.id == cid }) {
            var call = session.recentCalls[idx]
            call.inFlight = false
            call.durationMs = data.durationMs
            call.exitCode = data.exitCode
            call.resultSummary = data.resultSummary
            call.error = data.error
            call.status = data.status
            call.endedAt = data.endedAt
            session.recentCalls[idx] = call
        } else {
            // No matching start — codex.turn coarse case. Synthesize a
            // closed entry so operators see the activity.
            let synthetic = LiveToolCall(
                id: data.callID ?? "synthetic-\(UUID().uuidString)",
                toolName: data.toolName ?? "unknown",
                durationMs: data.durationMs,
                exitCode: data.exitCode,
                resultSummary: data.resultSummary,
                error: data.error,
                status: data.status,
                endedAt: data.endedAt,
                inFlight: false
            )
            session.recentCalls.insert(synthetic, at: 0)
            if session.recentCalls.count > liveSessionsRecentCallsLimit {
                session.recentCalls.removeLast(session.recentCalls.count - liveSessionsRecentCallsLimit)
            }
        }
        session.lastActivity = Date()
        sessionsByID[sid] = session
    }

    // MARK: - Plumbing

    private func decode(_ event: SSEEvent) -> LiveSessionEventEnvelope? {
        // Reconstruct the envelope from the SSE wire fields.
        // The daemon publishes JSON with `id`, `type`, `timestamp`, `data`
        // — but `SSEClient` parses out the `data:` payload. The HUD's
        // /events stream emits the raw envelope in the SSE `data:` field.
        guard let payload = event.data.data(using: .utf8) else { return nil }
        let decoder = JSONDecoder()
        do {
            return try decoder.decode(LiveSessionEventEnvelope.self, from: payload)
        } catch {
            return nil
        }
    }

    private func startReapTask() {
        reapTask?.cancel()
        reapTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                await MainActor.run { self?.reapEnded() }
            }
        }
    }

    private func reapEnded() {
        let cutoff = Date().addingTimeInterval(-liveSessionsEndedRetentionSeconds)
        for (sid, s) in sessionsByID {
            if let endedAt = s.endedAt, endedAt < cutoff {
                sessionsByID.removeValue(forKey: sid)
            }
        }
    }
}

private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
