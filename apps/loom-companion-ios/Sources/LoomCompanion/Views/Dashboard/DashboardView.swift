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
                    // Hero: the ONE thing to do next (scales to critical/all-clear).
                    // Critical alerts take top priority — they used to show as a
                    // separate red banner, now fold into this single anchor.
                    NextActionCard(
                        lanes: dashboard.coordination.attentionLanes,
                        health: dashboard.health,
                        criticalAlerts: alertsViewModel.criticalAlerts,
                        onNavigate: onNavigate
                    )
                    .cardAppear(index: 0)

                    // Remaining attention lanes — hero already represents #1
                    if dashboard.coordination.attentionLanes.count > 1 {
                        AttentionLanesCard(
                            lanes: dashboard.coordination.attentionLanes,
                            skipFirst: true
                        ) { lane in
                            HapticManager.selection()
                            onNavigate?(navigationAction(for: lane))
                        }
                        .cardAppear(index: 1)
                    }

                    // Active work — scales itself (compact when steady, standard when blocked)
                    if let counts = viewModel.taskCounts,
                       counts.pending + counts.inProgress + counts.blocked > 0 {
                        ActiveWorkCard(counts: counts) {
                            onNavigate?(.work)
                        }
                        .cardAppear(index: 2)
                    }

                    // Context: fleet (compact when steady, standard when anomaly)
                    Button {
                        HapticManager.selection()
                        onNavigate?(.people)
                    } label: {
                        FleetSummaryCard(dashboard: dashboard)
                    }
                    .buttonStyle(.plain)
                    .cardAppear(index: 3)

                    // Context: server health (compact when all-healthy)
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

                    if let agoText = updatedAgo {
                        HStack {
                            Spacer()
                            Text("Updated \(agoText)")
                                .font(LoomTypography.monoCaption)
                                .foregroundStyle(LoomColors.textTertiary)
                                .contentTransition(.numericText())
                            Spacer()
                        }
                        .padding(.top, LoomSpacing.xs)
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
        switch lane.route {
        case "people":
            return .people
        case "connection":
            return .connection
        default:
            return .work
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
