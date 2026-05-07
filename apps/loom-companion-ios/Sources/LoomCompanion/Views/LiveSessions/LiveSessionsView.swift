// LiveSessionsView.swift — Phase 5 of the spectator plan
// (`.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`).
//
// iOS-side mirror of the HUD `LiveSessionsCard` (Phase 3). Subscribes to
// the existing app-wide `SSEEventBroadcaster` and renders every active
// agent session with its trailing tool calls. Tap a row to drill into
// the full ring buffer.

import SwiftUI
import LoomCompanionKit

public struct LiveSessionsView: View {
    @State private var viewModel = LiveSessionsViewModel()
    @State private var selectedSessionID: String?

    private let broadcaster: SSEEventBroadcaster

    public init(broadcaster: SSEEventBroadcaster) {
        self.broadcaster = broadcaster
    }

    public var body: some View {
        NavigationStack {
            content
                .navigationTitle("Live Sessions")
                .navigationBarTitleDisplayMode(.inline)
                .task { viewModel.subscribe(to: broadcaster) }
        }
    }

    @ViewBuilder
    private var content: some View {
        let sessions = viewModel.visibleSessions
        if sessions.isEmpty {
            emptyState
        } else {
            List {
                Section {
                    summaryRow
                }

                Section("Sessions") {
                    ForEach(sessions, id: \.id) { session in
                        Button {
                            selectedSessionID = session.id
                        } label: {
                            sessionRow(session)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .listStyle(.insetGrouped)
            .sheet(item: Binding(
                get: { selectedSessionID.map { SessionIDWrapper(id: $0) } },
                set: { selectedSessionID = $0?.id }
            )) { wrapper in
                if let session = viewModel.sessionsByID[wrapper.id] {
                    LiveSessionDetailSheet(session: session)
                }
            }
        }
    }

    private var summaryRow: some View {
        HStack {
            Label("\(viewModel.activeSessionCount) active", systemImage: "circle.fill")
                .labelStyle(.titleAndIcon)
                .foregroundStyle(.green)
                .font(.subheadline)
            Spacer()
            if viewModel.inFlightCallCount > 0 {
                Label("\(viewModel.inFlightCallCount) in flight", systemImage: "bolt.horizontal.fill")
                    .labelStyle(.titleAndIcon)
                    .foregroundStyle(.orange)
                    .font(.subheadline)
            }
        }
    }

    private var emptyState: some View {
        ContentUnavailableView {
            Label("No active sessions", systemImage: "waveform.path.badge.minus")
        } description: {
            Text("Sessions appear here as soon as a Claude Code, Gemini, or Codex CLI emits a SessionStart hook. Public-tier event redaction is applied at the producer; secrets never reach this view.")
        }
    }

    @ViewBuilder
    private func sessionRow(_ session: LiveSession) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Circle()
                    .fill(color(for: session.agentStatus))
                    .frame(width: 8, height: 8)
                Text(session.agentID.isEmpty ? "(unknown agent)" : session.agentID)
                    .font(.headline)
                    .lineLimit(1)
                Spacer()
                Text(String(session.id.prefix(8)))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                if session.endedAt != nil {
                    Text("ended")
                        .font(.caption2)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.gray.opacity(0.2))
                        .clipShape(Capsule())
                        .foregroundStyle(.secondary)
                }
            }

            // Show up to 3 most-recent calls inline.
            ForEach(Array(session.recentCalls.prefix(3)), id: \.id) { call in
                HStack(spacing: 6) {
                    Circle()
                        .fill(color(for: call))
                        .frame(width: 6, height: 6)
                    Text(call.displayName)
                        .font(.caption.monospaced())
                        .lineLimit(1)
                    Spacer()
                    Text(formatDuration(call.durationMs))
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                }
            }

            if session.recentCalls.count > 3 {
                Text("+\(session.recentCalls.count - 3) more")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            } else if session.recentCalls.isEmpty {
                Text("Waiting for first tool call…")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .italic()
            }
        }
        .padding(.vertical, 4)
    }

    private func color(for status: LiveAgentStatus) -> Color {
        switch status {
        case .active: return .green
        case .idle: return .yellow
        case .offline, .expired: return .gray
        case .unknown: return .secondary
        }
    }

    private func color(for call: LiveToolCall) -> Color {
        if call.inFlight { return .blue }
        if call.error != nil { return .red }
        if let exit = call.exitCode, exit != 0 { return .red }
        return .green
    }

    private func formatDuration(_ ms: Int?) -> String {
        guard let ms else { return "—" }
        if ms < 1000 { return "\(ms)ms" }
        return String(format: "%.1fs", Double(ms) / 1000.0)
    }
}

/// SwiftUI requires Identifiable for `.sheet(item:)`; wrap a String session id.
private struct SessionIDWrapper: Identifiable { let id: String }

/// Detail sheet showing the full ring buffer for one session.
private struct LiveSessionDetailSheet: View {
    let session: LiveSession
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                agentSection
                callsSection
            }
            .navigationTitle("Session detail")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }

    private var agentSection: some View {
        Section("Agent") {
            LabeledContent("ID", value: session.agentID.isEmpty ? "(unknown)" : session.agentID)
            LabeledContent("Status", value: session.agentStatus.rawValue.capitalized)
            LabeledContent("Session", value: session.id)
                .font(.caption.monospaced())
        }
    }

    @ViewBuilder
    private var callsSection: some View {
        Section("Recent calls (\(session.recentCalls.count))") {
            if session.recentCalls.isEmpty {
                Text("No tool calls recorded yet.")
                    .foregroundStyle(.secondary)
                    .italic()
            } else {
                ForEach(session.recentCalls, id: \.id) { call in
                    LiveCallDetailRow(call: call)
                }
            }
        }
    }
}

private struct LiveCallDetailRow: View {
    let call: LiveToolCall

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(call.displayName)
                    .font(.subheadline.monospaced())
                Spacer()
                if call.inFlight {
                    Label("running", systemImage: "circle.dotted")
                        .font(.caption2)
                        .foregroundStyle(.blue)
                }
            }
            if let summary = call.resultSummary, !summary.isEmpty {
                Text(summary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let err = call.error, !err.isEmpty {
                Text(err)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
            metadataRow
        }
        .padding(.vertical, 2)
    }

    @ViewBuilder
    private var metadataRow: some View {
        HStack(spacing: 12) {
            if let dur = call.durationMs {
                Label(formatDuration(dur), systemImage: "stopwatch")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
            if let exit = call.exitCode {
                Label("exit \(exit)", systemImage: "arrow.up.forward.app")
                    .font(.caption2)
                    .foregroundStyle(exit == 0 ? Color.secondary : Color.red)
            }
        }
    }

    private func formatDuration(_ ms: Int) -> String {
        if ms < 1000 { return "\(ms)ms" }
        return String(format: "%.1fs", Double(ms) / 1000.0)
    }
}
