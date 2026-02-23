import SwiftUI
import LoomCompanionKit

struct DashboardView: View {
    @State private var viewModel: DashboardViewModel
    let healthMonitor: ConnectionHealthMonitor
    let alertsViewModel: AlertsViewModel
    @State private var showingAlerts = false

    init(apiClient: APIClient?, healthMonitor: ConnectionHealthMonitor, alertsViewModel: AlertsViewModel = AlertsViewModel()) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpAPIClient()
        self.alertsViewModel = alertsViewModel
        _viewModel = State(initialValue: DashboardViewModel(apiClient: client, alertsViewModel: alertsViewModel))
        self.healthMonitor = healthMonitor
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                ErrorBanner(health: healthMonitor.health)

                if !alertsViewModel.criticalAlerts.isEmpty {
                    criticalAlertBanner
                }

                if let dashboard = viewModel.dashboard {
                    HealthStatusCard(health: dashboard.health)
                    FleetSummaryCard(dashboard: dashboard)
                    TimelineListView(entries: dashboard.recentTimeline)
                } else if viewModel.isLoading {
                    ProgressView("Loading dashboard...")
                        .padding(.top, 40)
                } else if let error = viewModel.error {
                    ContentUnavailableView {
                        Label("Error", systemImage: "exclamationmark.triangle")
                    } description: {
                        Text(error.description)
                    } actions: {
                        Button("Retry") {
                            Task { await viewModel.load() }
                        }
                    }
                }
            }
            .padding()
        }
        .navigationTitle("Dashboard")
        .navigationDestination(isPresented: $showingAlerts) {
            AlertsListView(viewModel: alertsViewModel)
        }
        .refreshable {
            await viewModel.load()
        }
        .task {
            await viewModel.load()
        }
    }

    private var criticalAlertBanner: some View {
        Button {
            showingAlerts = true
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.white)

                VStack(alignment: .leading, spacing: 2) {
                    Text("\(alertsViewModel.criticalAlerts.count) Critical Alert\(alertsViewModel.criticalAlerts.count == 1 ? "" : "s")")
                        .font(.subheadline)
                        .fontWeight(.semibold)
                        .foregroundStyle(.white)

                    if let first = alertsViewModel.criticalAlerts.first {
                        Text(first.message)
                            .font(.caption)
                            .foregroundStyle(.white.opacity(0.9))
                            .lineLimit(1)
                    }
                }

                Spacer()

                Image(systemName: "chevron.right")
                    .font(.caption)
                    .foregroundStyle(.white.opacity(0.7))
            }
            .padding(12)
            .background(.red, in: RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
    }
}

/// No-op client used when no real client is available yet.
private struct NoOpAPIClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
