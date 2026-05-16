import SwiftUI
import LoomCompanionKit

struct DashboardView: View {
    @State private var viewModel: DashboardViewModel
    let healthMonitor: ConnectionHealthMonitor
    let alertsViewModel: AlertsViewModel
    let broadcaster: SSEEventBroadcaster?
    var onNavigate: ((DashboardNavAction) -> Void)?
    @State private var showingAgents = false
    @State private var showingConnection = false
    @State private var updatedAgo: String?
    @State private var refreshTimer: Timer?
    @Environment(\.navigationCoordinator) private var navigationCoordinator

    enum DashboardNavAction {
        case people
        case work
        case connection
        case liveActivities
        case alerts
    }

    init(apiClient: APIClient?, healthMonitor: ConnectionHealthMonitor, alertsViewModel: AlertsViewModel = AlertsViewModel(), broadcaster: SSEEventBroadcaster? = nil, onNavigate: ((DashboardNavAction) -> Void)? = nil) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpAPIClient()
        self.alertsViewModel = alertsViewModel
        self.broadcaster = broadcaster
        self.onNavigate = onNavigate
        _viewModel = State(initialValue: DashboardViewModel(apiClient: client, alertsViewModel: alertsViewModel))
        self.healthMonitor = healthMonitor
    }

    var body: some View {
        ScrollView {
            VStack(spacing: LoomSpacing.lg) {
                ErrorBanner(health: healthMonitor.health)

                // Triage-first header — mirrors the HUD A2 inbox strip.
                // Operator's at-a-glance answer to "do I need to do anything?"
                if viewModel.dashboard != nil {
                    DashboardInboxHeader(
                        pressureCount: pressureCount,
                        topSeverity: topSeverity,
                        updatedAgo: updatedAgo
                    )
                }

                // Critical alerts are now folded into NextActionCard as the
                // highest-priority action — no separate banner needed.

                #if os(iOS)
                if #available(iOS 16.2, *) {
                    let lam = LiveActivityManager.shared
                    if lam.activeCount > 0 {
                        LiveActivityBanner(
                            sessionCount: lam.activeSessionCount,
                            workflowCount: lam.activeWorkflowCount,
                            pipelineCount: lam.activePipelineCount
                        ) {
                            onNavigate?(.liveActivities)
                        }
                    }
                }
                #endif

                if let dashboard = viewModel.dashboard {
                    if isClear {
                        clearState(dashboard: dashboard)
                    } else {
                        pressureState(dashboard: dashboard)
                    }
                } else if viewModel.isLoading {
                    VStack(spacing: LoomSpacing.lg) {
                        SkeletonHeroCard()
                            .cardAppear(index: 0)
                        SkeletonDashboardCard()
                            .cardAppear(index: 1)
                        SkeletonCompactRow()
                            .cardAppear(index: 2)
                        SkeletonCompactRow()
                            .cardAppear(index: 3)
                    }
                } else if let error = viewModel.error {
                    ContentUnavailableView {
                        Label(error.dashboardTitle, systemImage: "wifi.exclamationmark")
                    } description: {
                        Text(error.description)
                    } actions: {
                        Button("Retry") {
                            Task { await refreshDashboard() }
                        }
                        .buttonStyle(.borderedProminent)

                        if error.shouldSuggestConnectionTab {
                            Button("Open Connection") {
                                onNavigate?(.connection)
                            }
                            .buttonStyle(.bordered)
                        }
                    }
                } else {
                    ContentUnavailableView {
                        Label("No Dashboard Data", systemImage: "square.grid.2x2")
                    } description: {
                        Text("Connect to a Loom server to view health, fleet, and task data. Check your connection settings in the Connection tab.")
                    } actions: {
                        Button("Refresh") {
                            Task { await refreshDashboard() }
                        }
                        .buttonStyle(.borderedProminent)
                    }
                }
            }
            .padding()
        }
        .safeAreaInset(edge: .bottom) {
            Color.clear
                .frame(height: 96)
                .allowsHitTesting(false)
        }
        .navigationTitle("Dashboard")
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .onChange(of: viewModel.dashboard?.updatedAt) { _, newValue in
            if let ts = newValue {
                withAnimation { updatedAgo = Self.relativeTime(ts) }
            }
        }
        .onAppear {
            // Auto-refresh the "Updated Xs ago" label every 5 seconds.
            refreshTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { _ in
                Task { @MainActor in
                    if let ts = viewModel.dashboard?.updatedAt {
                        withAnimation { updatedAgo = Self.relativeTime(ts) }
                    }
                }
            }
        }
        .onDisappear {
            refreshTimer?.invalidate()
            refreshTimer = nil
        }
        .refreshable {
            await refreshDashboard()
            HapticManager.light()
        }
        .task {
            healthMonitor.onPollRefresh = {
                await refreshDashboard()
            }

            await refreshDashboard()

            if let broadcaster {
                viewModel.startListening(broadcaster: broadcaster)
            }
        }
    }

    // MARK: - Triage-First Decomposition (HUD A2 alignment)

    /// Count of distinct pressure points the operator should consider. Mirrors
    /// the HUD inbox count: each attention lane + each unread critical alert.
    /// Health degradations are already projected into lanes by the backend, so
    /// we don't double-count them here.
    private var pressureCount: Int {
        guard let dashboard = viewModel.dashboard else { return 0 }
        let lanes = dashboard.coordination.attentionLanes.count
        let unreadCritical = alertsViewModel.criticalAlerts.filter { !$0.isRead }.count
        return lanes + unreadCritical
    }

    /// Worst severity present across alerts + lanes. Drives the count-pill tint.
    private var topSeverity: DashboardInboxHeader.Severity {
        guard let dashboard = viewModel.dashboard else { return .nominal }
        let unreadCritical = alertsViewModel.criticalAlerts.filter { !$0.isRead }
        if !unreadCritical.isEmpty { return .critical }
        if dashboard.coordination.attentionLanes.contains(where: { $0.severity == "critical" }) {
            return .critical
        }
        if dashboard.coordination.attentionLanes.contains(where: { $0.severity == "warning" }) {
            return .warning
        }
        if !dashboard.coordination.attentionLanes.isEmpty { return .info }
        return .nominal
    }

    private var isClear: Bool {
        pressureCount == 0
    }

    // MARK: - Clear state (hide-when-clear)

    /// When nothing needs attention, the dashboard becomes a single calm anchor
    /// + the compact fleet chip. Everything else recedes. This is the operator
    /// "the world is fine" surface — the opposite of glance-overload.
    @ViewBuilder
    private func clearState(dashboard: DashboardData) -> some View {
        LoomEmptyState(
            tone: .nominal,
            title: "System nominal",
            detail: clearDetail(dashboard: dashboard)
        )
        .loomCard(priority: .standard)
        .cardAppear(index: 0)

        Button {
            HapticManager.selection()
            onNavigate?(.people)
        } label: {
            FleetSummaryCard(dashboard: dashboard)
        }
        .buttonStyle(.plain)
        .cardAppear(index: 1)
    }

    /// Short mono-styled detail line under "System nominal", composed from the
    /// fleet numbers the operator would otherwise check by scrolling.
    private func clearDetail(dashboard: DashboardData) -> String {
        var parts: [String] = []
        parts.append("\(dashboard.activeAgents) agent\(dashboard.activeAgents == 1 ? "" : "s") active")
        parts.append("\(dashboard.activeSessions) session\(dashboard.activeSessions == 1 ? "" : "s")")
        if dashboard.offlineAgents > 0 {
            parts.append("\(dashboard.offlineAgents) offline")
        }
        return parts.joined(separator: " · ")
    }

    // MARK: - Pressure state (triage queue + collapsed context)

    /// When there's work to do, surface the inbox stack — hero, attention
    /// queue, active work — and demote steady-state context (fleet, health,
    /// timeline) below a subtle "Context" divider so they don't compete.
    @ViewBuilder
    private func pressureState(dashboard: DashboardData) -> some View {
        NextActionCard(
            lanes: dashboard.coordination.attentionLanes,
            health: dashboard.health,
            criticalAlerts: alertsViewModel.criticalAlerts,
            onNavigate: onNavigate,
            onLaneNavigate: routeFromLane
        )
        .cardAppear(index: 0)

        if dashboard.coordination.attentionLanes.count > 1 {
            AttentionLanesCard(
                lanes: dashboard.coordination.attentionLanes,
                skipFirst: true
            ) { lane in
                HapticManager.selection()
                routeFromLane(lane)
            }
            .cardAppear(index: 1)
        }

        if let counts = viewModel.taskCounts,
           counts.pending + counts.inProgress + counts.blocked > 0 {
            ActiveWorkCard(counts: counts) {
                onNavigate?(.work)
            }
            .cardAppear(index: 2)
        }

        // Steady-state context — recedes below the triage queue.
        contextDivider

        Button {
            HapticManager.selection()
            onNavigate?(.people)
        } label: {
            FleetSummaryCard(dashboard: dashboard)
        }
        .buttonStyle(.plain)
        .cardAppear(index: 3)

        Button {
            HapticManager.selection()
            onNavigate?(.connection)
        } label: {
            HealthStatusCard(health: dashboard.health)
        }
        .buttonStyle(.plain)
        .cardAppear(index: 4)

        TimelineListView(entries: dashboard.recentTimeline)
            .cardAppear(index: 5)
    }

    /// Subtle "below the fold" divider that signals what follows is context,
    /// not action. Uses the kindLabel motif for visual continuity with the
    /// inbox header.
    private var contextDivider: some View {
        HStack(spacing: LoomSpacing.sm) {
            Rectangle()
                .fill(LoomColors.border)
                .frame(height: 1)
            Text("CONTEXT")
                .font(LoomTypography.kindLabel)
                .tracking(LoomTypography.kindLabelTracking)
                .foregroundStyle(LoomColors.fgMuted)
            Rectangle()
                .fill(LoomColors.border)
                .frame(height: 1)
        }
        .padding(.top, LoomSpacing.xs)
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

    private static func relativeTime(_ iso: String) -> String? {
        guard let date = isoFormatter.date(from: iso) ?? isoFallback.date(from: iso) else { return nil }
        let diff = Int(Date().timeIntervalSince(date))
        if diff < 0 { return "just now" }
        if diff < 5 { return "just now" }
        if diff < 60 { return "\(diff)s ago" }
        if diff < 3600 { return "\(diff / 60)m ago" }
        return "\(diff / 3600)h ago"
    }

    private func navigationAction(for lane: DashboardAttentionLane) -> DashboardNavAction {
        if lane.isTaskLane {
            return .work
        }

        switch lane.route {
        case "people":
            return .people
        case "connection":
            return .connection
        default:
            return .work
        }
    }

    private func routeFromLane(_ lane: DashboardAttentionLane) {
        if let url = URL(string: lane.deepLink),
           let link = DeepLink.from(url) {
            route(link)
            return
        }

        if lane.isTaskLane {
            navigationCoordinator?.filterTasks(
                status: lane.taskStatusHint,
                agentId: lane.filter?.agentId,
                sessionId: lane.filter?.sessionId
            )
            onNavigate?(.work)
            return
        }

        switch lane.targetKind {
        case "session":
            if !lane.targetId.isEmpty {
                navigationCoordinator?.navigateToSession(id: lane.targetId)
                return
            }
        case "agent":
            if !lane.targetId.isEmpty {
                navigationCoordinator?.navigateToAgent(id: lane.targetId)
                return
            }
        case "task_filter":
            navigationCoordinator?.filterTasks(
                status: lane.filter?.status,
                agentId: lane.filter?.agentId,
                sessionId: lane.filter?.sessionId
            )
            onNavigate?(.work)
            return
        case "workflow":
            if !lane.targetId.isEmpty {
                navigationCoordinator?.navigateToWorkflow(id: lane.targetId)
                return
            }
        case "spawn":
            if !lane.targetId.isEmpty {
                navigationCoordinator?.navigateToSpawn(id: lane.targetId)
                return
            }
        case "handoff":
            navigationCoordinator?.openHandoffInbox()
            onNavigate?(.work)
            return
        case "connection":
            onNavigate?(.connection)
            return
        case "alert":
            if !lane.targetId.isEmpty {
                navigationCoordinator?.navigateToAlert(id: lane.targetId)
            }
            onNavigate?(.alerts)
            return
        default:
            break
        }
        onNavigate?(navigationAction(for: lane))
    }

    private func route(_ link: DeepLink) {
        switch link {
        case .dashboard:
            break
        case .people:
            onNavigate?(.people)
        case .work:
            onNavigate?(.work)
        case .alerts:
            onNavigate?(.alerts)
        case .connection, .configure:
            onNavigate?(.connection)
        case .session(let id):
            navigationCoordinator?.navigateToSession(id: id)
        case .agent(let id):
            navigationCoordinator?.navigateToAgent(id: id)
        case .sessions(let status, let agentId):
            navigationCoordinator?.filterSessions(status: status, agentId: agentId)
            onNavigate?(.people)
        case .agents(let status, let type):
            navigationCoordinator?.filterAgents(status: status, type: type)
            onNavigate?(.people)
        case .tasks(let status, let agentId, let sessionId):
            navigationCoordinator?.filterTasks(status: status, agentId: agentId, sessionId: sessionId)
            onNavigate?(.work)
        case .workflow(let id, _):
            navigationCoordinator?.navigateToWorkflow(id: id)
        case .spawn(let id):
            navigationCoordinator?.navigateToSpawn(id: id)
        case .handoff:
            navigationCoordinator?.openHandoffInbox()
            onNavigate?(.work)
        case .alert(let id):
            navigationCoordinator?.navigateToAlert(id: id)
            onNavigate?(.alerts)
        }
    }

    @MainActor
    private func refreshDashboard() async {
        await viewModel.load()
        if let error = viewModel.error {
            healthMonitor.handleAPIError(error)
        } else {
            healthMonitor.handleSuccess()
        }
    }

}

private struct NoOpAPIClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
