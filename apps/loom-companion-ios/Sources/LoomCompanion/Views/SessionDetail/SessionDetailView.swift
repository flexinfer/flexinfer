import SwiftUI
import LoomCompanionKit

struct SessionDetailView: View {
    let sessionId: String
    @State private var viewModel: SessionDetailViewModel
    @State private var showingEndConfirmation = false
    @State private var showingEndError = false
    @State private var liveActivityStarted = false
    @Environment(\.navigationCoordinator) private var navigationCoordinator

    // DisclosureGroup expansion state: first two default expanded
    @State private var entriesExpanded = true
    @State private var tasksExpanded = true
    @State private var decisionsExpanded = false
    @State private var errorsExpanded = false
    @State private var filesExpanded = false
    @State private var eventsExpanded = false

    init(sessionId: String, apiClient: any LoomAPIClientProtocol) {
        self.sessionId = sessionId
        _viewModel = State(initialValue: SessionDetailViewModel(apiClient: apiClient))
    }

    private var isActive: Bool {
        viewModel.session?.status == .active
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if let session = viewModel.session {
                    SessionMetadataView(session: session)
                        .cardAppear(index: 0)

                    hierarchyActions(for: session)
                        .cardAppear(index: 1)

                    // Cross-tab link to the agent that owns this session
                    if !session.agentId.isEmpty {
                        Button {
                            HapticManager.selection()
                            navigationCoordinator?.navigateToAgent(id: session.agentId)
                        } label: {
                            HStack(spacing: 6) {
                                Image(systemName: "person.crop.circle")
                                    .font(.system(size: 12))
                                Text("View Agent: \(session.agentId)")
                                    .font(LoomTypography.caption)
                                    .lineLimit(1)
                                Spacer()
                                Image(systemName: "chevron.right")
                                    .font(.caption2)
                                    .foregroundStyle(LoomColors.textTertiary)
                            }
                            .foregroundStyle(LoomColors.info)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 8)
                            .background(LoomColors.infoDim, in: RoundedRectangle(cornerRadius: 8))
                        }
                        .buttonStyle(.plain)
                    }

                    // Context entry breakdown (collapsible with summary)
                    if !viewModel.entryBreakdown.isEmpty {
                        collapsibleSection(
                            isExpanded: $entriesExpanded,
                            icon: "chart.bar",
                            title: "Entry Breakdown",
                            summary: entrySummary,
                            index: 1
                        ) {
                            SessionEntryBreakdownView(buckets: viewModel.entryBreakdown)
                        }
                    }

                    // Tasks summary (collapsible with summary)
                    if let tasks = viewModel.tasks, tasks.total > 0 {
                        collapsibleSection(
                            isExpanded: $tasksExpanded,
                            icon: "checklist",
                            title: "Tasks",
                            summary: tasksSummary(tasks),
                            index: 2
                        ) {
                            SessionTasksView(tasks: tasks)
                        }
                    }

                    if let activity = viewModel.activity,
                       activity.taskCount > 0 || activity.pipelineCount > 0 {
                        sessionActivityCard(activity)
                            .cardAppear(index: 3)
                    }

                    // Decisions (collapsible with summary)
                    if !viewModel.decisions.isEmpty {
                        collapsibleSection(
                            isExpanded: $decisionsExpanded,
                            icon: "lightbulb",
                            title: "Top Decisions",
                            summary: decisionsSummary,
                            index: 3
                        ) {
                            SessionEntriesSection(
                                title: "Decisions",
                                icon: "lightbulb",
                                entries: viewModel.decisions
                            )
                        }
                    }

                    // Errors (collapsible with summary)
                    if !viewModel.errors.isEmpty {
                        collapsibleSection(
                            isExpanded: $errorsExpanded,
                            icon: "exclamationmark.triangle",
                            title: "Errors",
                            summary: errorsSummary,
                            index: 4
                        ) {
                            SessionEntriesSection(
                                title: "Errors",
                                icon: "exclamationmark.triangle",
                                entries: viewModel.errors
                            )
                        }
                    }

                    // Top files (collapsible with summary)
                    if !viewModel.topFiles.isEmpty {
                        collapsibleSection(
                            isExpanded: $filesExpanded,
                            icon: "doc.text",
                            title: "Top Files",
                            summary: filesSummary,
                            index: 5
                        ) {
                            SessionTopFilesView(files: viewModel.topFiles)
                        }
                    }

                    // Events timeline (collapsible with summary)
                    collapsibleSection(
                        isExpanded: $eventsExpanded,
                        icon: "clock.arrow.circlepath",
                        title: "Events",
                        summary: "\(viewModel.events.count) events",
                        index: 6
                    ) {
                        SessionEventsView(events: viewModel.events)
                    }
                } else if viewModel.isLoading {
                    ProgressView("Loading session...")
                        .padding(.top, 40)
                } else if let error = viewModel.error {
                    ContentUnavailableView {
                        Label("Error", systemImage: "exclamationmark.triangle")
                    } description: {
                        Text(error.description)
                    } actions: {
                        Button("Retry") {
                            Task { await viewModel.load(sessionId: sessionId) }
                        }
                    }
                }
            }
            .padding()
        }
        .navigationTitle("Session")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    LoomCopyLinkButton(link: .session(id: sessionId))
                    LoomShareLink(link: .session(id: sessionId))
                } label: {
                    Label("Share", systemImage: "square.and.arrow.up")
                }
            }
            ToolbarItemGroup(placement: .primaryAction) {
                if isActive {
                    #if os(iOS)
                    if #available(iOS 16.2, *) {
                        Button {
                            HapticManager.light()
                            startLiveActivity()
                        } label: {
                            Label(
                                liveActivityStarted ? "Activity Running" : "Live Activity",
                                systemImage: liveActivityStarted
                                    ? "dot.radiowaves.left.and.right"
                                    : "dot.radiowaves.left.and.right"
                            )
                            .symbolEffect(.pulse, isActive: liveActivityStarted)
                        }
                        .tint(liveActivityStarted ? LoomColors.statusHealthy : nil)
                    }
                    #endif

                    if viewModel.isEnding {
                        ProgressView()
                    } else {
                        Button("End Session", role: .destructive) {
                            showingEndConfirmation = true
                        }
                    }
                }
            }
        }
        .confirmationDialog(
            "End Session?",
            isPresented: $showingEndConfirmation,
            titleVisibility: .visible
        ) {
            Button("End Session", role: .destructive) {
                Task { await viewModel.endSession(summarize: false) }
            }
            Button("End with Summary", role: .destructive) {
                Task { await viewModel.endSession(summarize: true) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This will terminate the session. You can optionally generate a summary.")
        }
        .alert("Failed to End Session", isPresented: $showingEndError) {
            Button("OK") { viewModel.endError = nil }
        } message: {
            Text(viewModel.endError ?? "Unknown error")
        }
        .onChange(of: viewModel.endError) { _, newValue in
            showingEndError = newValue != nil
        }
        .refreshable {
            await viewModel.load(sessionId: sessionId)
        }
        .task {
            await viewModel.load(sessionId: sessionId)
            updateLiveActivityState()
        }
    }

    // MARK: - Collapsible Section Builder

    private func collapsibleSection<Content: View>(
        isExpanded: Binding<Bool>,
        icon: String,
        title: String,
        summary: String,
        index: Int,
        @ViewBuilder content: @escaping () -> Content
    ) -> some View {
        DisclosureGroup(isExpanded: isExpanded) {
            content()
        } label: {
            HStack {
                Label(title, systemImage: icon)
                    .font(.headline)
                Spacer()
                Text(summary)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textSecondary)
            }
        }
        .animation(.spring(duration: 0.35), value: isExpanded.wrappedValue)
        .cardAppear(index: index)
    }

    // MARK: - Summary Strings

    private var entrySummary: String {
        let totalEntries = viewModel.entryBreakdown.reduce(0) { $0 + $1.count }
        let totalTokens = viewModel.entryBreakdown.reduce(0) { $0 + $1.estimatedTokens }
        return "\(totalEntries) entries \u{00B7} \(formatTokenCount(totalTokens))"
    }

    private func tasksSummary(_ tasks: SessionTaskSummary) -> String {
        var parts: [String] = []
        if tasks.pending > 0 { parts.append("\(tasks.pending) pending") }
        if tasks.inProgress > 0 { parts.append("\(tasks.inProgress) in-progress") }
        if tasks.completed > 0 { parts.append("\(tasks.completed) done") }
        return parts.isEmpty ? "\(tasks.total) tasks" : parts.joined(separator: " \u{00B7} ")
    }

    @ViewBuilder
    private func hierarchyActions(for session: SessionInfo) -> some View {
        let parentId = session.parentSessionId?.trimmingCharacters(in: .whitespacesAndNewlines)
        let rootId = session.rootSessionId?.trimmingCharacters(in: .whitespacesAndNewlines)
        if (parentId?.isEmpty == false) || (rootId?.isEmpty == false && rootId != session.id) {
            LoomCard(priority: .compact) {
                HStack(spacing: 8) {
                    Label("Hierarchy", systemImage: "point.3.connected.trianglepath.dotted")
                        .font(LoomTypography.labelLarge)
                        .foregroundStyle(LoomColors.fgPrimary)
                    Spacer()
                    if let parentId, !parentId.isEmpty {
                        Button {
                            navigationCoordinator?.navigateToSession(id: parentId)
                        } label: {
                            Label("Parent", systemImage: "arrow.uturn.left")
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }
                    if let rootId, !rootId.isEmpty, rootId != session.id {
                        Button {
                            navigationCoordinator?.navigateToSession(id: rootId)
                        } label: {
                            Label("Root", systemImage: "arrow.up.left.and.arrow.down.right")
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                    }
                }
            }
        }
    }

    private func sessionActivityCard(_ activity: SessionActivityResponse) -> some View {
        LoomCard(
            priority: activity.hasFailedPipeline ? .standard : .compact,
            accent: activity.hasFailedPipeline ? .severity(LoomColors.statusCritical, pulse: false) : .none
        ) {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                HStack {
                    Label("Live Activity", systemImage: "bolt.horizontal")
                        .font(LoomTypography.labelLarge)
                        .foregroundStyle(LoomColors.fgPrimary)
                    Spacer()
                    if activity.hasFailedPipeline {
                        LoomPill("pipeline failed", icon: "xmark.octagon", color: LoomColors.statusCritical, weight: .micro)
                    }
                }
                HStack(spacing: LoomSpacing.sm) {
                    LoomPill("\(activity.taskCount) tasks", icon: "checklist", color: LoomColors.accent, style: .outlined, weight: .micro)
                    LoomPill("\(activity.pipelineCount) pipelines", icon: "arrow.triangle.2.circlepath", color: activity.hasFailedPipeline ? LoomColors.statusCritical : LoomColors.statusInfo, style: .outlined, weight: .micro)
                }
                if let failed = activity.pipelines.first(where: { $0.status.lowercased() == "failed" || $0.failedJobCount > 0 }) {
                    Text("\(failed.project) · \(failed.ref) · \(failed.failedJobCount) failed jobs")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.fgSecondary)
                        .lineLimit(2)
                }
            }
        }
    }

    private var decisionsSummary: String {
        let count = viewModel.decisions.count
        let latestAge = latestRelativeTime(viewModel.decisions)
        if let age = latestAge {
            return "\(count) decisions \u{00B7} latest \(age)"
        }
        return "\(count) decisions"
    }

    private var errorsSummary: String {
        let count = viewModel.errors.count
        let latestAge = latestRelativeTime(viewModel.errors)
        if let age = latestAge {
            return "\(count) errors \u{00B7} latest \(age)"
        }
        return "\(count) errors"
    }

    private var filesSummary: String {
        "\(viewModel.topFiles.count) files touched"
    }

    // MARK: - Helpers

    private func formatTokenCount(_ tokens: Int) -> String {
        if tokens >= 1000 {
            return String(format: "%.1fk tokens", Double(tokens) / 1000.0)
        }
        return "\(tokens) tokens"
    }

    private static let isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let isoFallback: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private func latestRelativeTime(_ entries: [SessionTopEntry]) -> String? {
        let dates = entries.compactMap {
            Self.isoFormatter.date(from: $0.timestamp) ?? Self.isoFallback.date(from: $0.timestamp)
        }
        guard let latest = dates.max() else { return nil }
        let diff = Int(Date().timeIntervalSince(latest))
        if diff < 60 { return "\(diff)s ago" }
        if diff < 3600 { return "\(diff / 60)m ago" }
        return "\(diff / 3600)h ago"
    }

    private func updateLiveActivityState() {
        #if os(iOS)
        if #available(iOS 16.2, *) {
            liveActivityStarted = LiveActivityManager.shared.hasSessionActivity(sessionId: sessionId)
        }
        #endif
    }

    #if os(iOS)
    @available(iOS 16.2, *)
    private func startLiveActivity() {
        guard let session = viewModel.session else { return }

        let agentType: String
        let agentId = session.agentId
        let lowered = agentId.lowercased()
        if lowered.contains("claude") { agentType = "claude-code" }
        else if lowered.contains("gemini") { agentType = "gemini" }
        else if lowered.contains("codex") { agentType = "codex" }
        else { agentType = "unknown" }

        LiveActivityManager.shared.startSessionActivity(
            sessionId: session.id,
            agentId: agentId,
            agentType: agentType,
            namespace: session.namespace
        )

        withAnimation(.spring(duration: 0.3)) {
            liveActivityStarted = true
        }
        HapticManager.success()
    }
    #endif
}
