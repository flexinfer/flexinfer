import SwiftUI
import LoomCompanionKit

struct SessionDetailView: View {
    let sessionId: String
    @State private var viewModel: SessionDetailViewModel

    init(sessionId: String, apiClient: any LoomAPIClientProtocol) {
        self.sessionId = sessionId
        _viewModel = State(initialValue: SessionDetailViewModel(apiClient: apiClient))
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if let session = viewModel.session {
                    SessionMetadataView(session: session)
                    SessionEventsView(events: viewModel.events)
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
        .refreshable {
            await viewModel.load(sessionId: sessionId)
        }
        .task {
            await viewModel.load(sessionId: sessionId)
        }
    }
}
