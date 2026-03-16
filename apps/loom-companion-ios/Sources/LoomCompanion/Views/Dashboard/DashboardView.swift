import SwiftUI
import LoomCompanionKit

struct DashboardView: View {
    @State private var viewModel: DashboardViewModel
    let healthMonitor: ConnectionHealthMonitor
    let alertsViewModel: AlertsViewModel
    let broadcaster: SSEEventBroadcaster?
    @State private var showingAlerts = false

    init(apiClient: APIClient?, healthMonitor: ConnectionHealthMonitor, alertsViewModel: AlertsViewModel = AlertsViewModel(), broadcaster: SSEEventBroadcaster? = nil) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpAPIClient()
        self.alertsViewModel = alertsViewModel
        self.broadcaster = broadcaster
        _viewModel = State(initialValue: DashboardViewModel(apiClient: client, alertsViewModel: alertsViewModel))
        self.healthMonitor = healthMonitor
    }

    var body: some View {
        ScrollView {
            VStack(spacing: LoomSpacing.lg) {
                ErrorBanner(health: healthMonitor.health)

                if !alertsViewModel.criticalAlerts.isEmpty {
                    criticalAlertBanner
                        .transition(.slideInFromTop)
                }

                if let dashboard = viewModel.dashboard {
                    HealthStatusCard(health: dashboard.health)
                        .cardAppear(index: 0)
                    FleetSummaryCard(dashboard: dashboard)
                        .cardAppear(index: 1)

                    if let counts = viewModel.taskCounts,
                       counts.pending + counts.inProgress + counts.blocked > 0 {
                        ActiveWorkCard(counts: counts)
                            .cardAppear(index: 2)
                    }

                    #if canImport(Charts)
                    if !dashboard.recentTimeline.isEmpty {
                        LoomCard {
                            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                                Text("Event Activity")
                                    .font(LoomTypography.headlineMedium)
                                    .foregroundStyle(LoomColors.textPrimary)
                                SessionTimelineChart(entries: dashboard.recentTimeline)
                            }
                        }
                        .cardAppear(index: 3)
                    }
                    #endif

                    TimelineListView(entries: dashboard.recentTimeline)
                        .cardAppear(index: 4)

                    if let agoText = Self.relativeTime(dashboard.updatedAt) {
                        HStack {
                            Spacer()
                            Text("Updated \(agoText)")
                                .font(LoomTypography.monoCaption)
                                .foregroundStyle(LoomColors.textTertiary)
                            Spacer()
                        }
                        .padding(.top, LoomSpacing.xs)
                    }
                } else if viewModel.isLoading {
                    VStack(spacing: LoomSpacing.lg) {
                        SkeletonDashboardCard()
                            .cardAppear(index: 0)
                        SkeletonDashboardCard()
                            .cardAppear(index: 1)
                        SkeletonDashboardCard()
                            .cardAppear(index: 2)
                    }
                } else if let error = viewModel.error {
                    ContentUnavailableView {
                        Label("Connection Error", systemImage: "wifi.exclamationmark")
                    } description: {
                        Text(error.description)
                    } actions: {
                        Button("Retry") {
                            Task { await viewModel.load() }
                        }
                        .buttonStyle(.borderedProminent)
                    }
                } else {
                    ContentUnavailableView {
                        Label("No Dashboard Data", systemImage: "square.grid.2x2")
                    } description: {
                        Text("Connect to a Loom server to view health, fleet, and task data. Check your connection settings in the Connection tab.")
                    } actions: {
                        Button("Refresh") {
                            Task { await viewModel.load() }
                        }
                        .buttonStyle(.borderedProminent)
                    }
                }
            }
            .padding()
            .animation(.spring(duration: 0.4), value: alertsViewModel.criticalAlerts.isEmpty)
        }
        .navigationTitle("Dashboard")
        .navigationDestination(isPresented: $showingAlerts) {
            AlertsListView(viewModel: alertsViewModel)
        }
        .refreshable {
            await viewModel.load()
            HapticManager.light()
        }
        .task {
            healthMonitor.onPollRefresh = { [weak viewModel] in
                await viewModel?.load()
            }

            await viewModel.load()

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

    private var criticalAlertBanner: some View {
        Button {
            HapticManager.medium()
            showingAlerts = true
        } label: {
            HStack(spacing: LoomSpacing.sm) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.white)
                    .symbolEffect(.pulse, isActive: true)

                VStack(alignment: .leading, spacing: 2) {
                    Text("\(alertsViewModel.criticalAlerts.count) Critical Alert\(alertsViewModel.criticalAlerts.count == 1 ? "" : "s")")
                        .font(LoomTypography.bodyMedium)
                        .foregroundStyle(.white)

                    if let first = alertsViewModel.criticalAlerts.first {
                        Text(first.message)
                            .font(LoomTypography.caption)
                            .foregroundStyle(.white.opacity(0.9))
                            .lineLimit(1)
                    }
                }

                Spacer()

                Image(systemName: "chevron.right")
                    .font(.caption)
                    .foregroundStyle(.white.opacity(0.7))
            }
            .padding(LoomSpacing.md)
            .background(.red, in: RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
    }
}

private struct NoOpAPIClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
