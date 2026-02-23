import SwiftUI
import LoomCompanionKit

struct SessionsListView: View {
    @State private var viewModel: SessionsViewModel
    private let apiClient: any LoomAPIClientProtocol

    init(apiClient: APIClient?) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpClient()
        self.apiClient = client
        _viewModel = State(initialValue: SessionsViewModel(apiClient: client))
    }

    var body: some View {
        List {
            SessionFilterView(
                statusFilter: $viewModel.statusFilter,
                agentFilter: $viewModel.agentFilter,
                availableAgents: viewModel.availableAgents
            )

            ForEach(viewModel.filteredSessions) { session in
                NavigationLink(value: session.id) {
                    SessionRowView(session: session)
                }
            }
        }
        .navigationTitle("Sessions")
        .navigationDestination(for: String.self) { sessionId in
            SessionDetailView(sessionId: sessionId, apiClient: apiClient)
        }
        .searchable(text: $viewModel.searchText, prompt: "Search sessions")
        .refreshable {
            await viewModel.load()
        }
        .overlay {
            if viewModel.isLoading && viewModel.sessions.isEmpty {
                ProgressView("Loading sessions...")
            } else if let error = viewModel.error, viewModel.sessions.isEmpty {
                ContentUnavailableView {
                    Label("Error", systemImage: "exclamationmark.triangle")
                } description: {
                    Text(error.description)
                } actions: {
                    Button("Retry") {
                        Task { await viewModel.load() }
                    }
                }
            } else if viewModel.filteredSessions.isEmpty && !viewModel.sessions.isEmpty {
                ContentUnavailableView.search(text: viewModel.searchText)
            }
        }
        .task {
            await viewModel.load()
        }
    }
}

private struct NoOpClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
