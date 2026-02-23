import SwiftUI
import LoomCompanionKit

struct DashboardView: View {
    @State private var viewModel: DashboardViewModel
    let healthMonitor: ConnectionHealthMonitor

    init(apiClient: APIClient?, healthMonitor: ConnectionHealthMonitor) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpAPIClient()
        _viewModel = State(initialValue: DashboardViewModel(apiClient: client))
        self.healthMonitor = healthMonitor
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                ErrorBanner(health: healthMonitor.health)

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
        .refreshable {
            await viewModel.load()
        }
        .task {
            await viewModel.load()
        }
    }
}

/// No-op client used when no real client is available yet.
private struct NoOpAPIClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
