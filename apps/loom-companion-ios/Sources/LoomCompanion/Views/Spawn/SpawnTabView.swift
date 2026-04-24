import SwiftUI
import LoomCompanionKit

/// Root view for the Spawn tab. Hosts SpawnAgentView as a first-class surface
/// (previously reachable only via Ops → Runtime → "Launch Runtime" and the
/// Work-tab Session-controls "Spawn a new agent" CTA — both removed in this
/// slice). Kept as a thin wrapper so later phases can replace the body with
/// Quick-Spawn / Templates / richer Active-Spawns UX without touching
/// ContentView's tab wiring again.
struct SpawnTabView: View {
    let apiClient: APIClient?
    var broadcaster: SSEEventBroadcaster?

    @State private var viewModel: SpawnViewModel

    init(apiClient: APIClient?, broadcaster: SSEEventBroadcaster? = nil) {
        self.apiClient = apiClient
        self.broadcaster = broadcaster
        let client = apiClient ?? APIClient(baseURL: URL(string: "http://localhost")!, token: "mock-token")
        _viewModel = State(initialValue: SpawnViewModel(apiClient: client))
    }

    var body: some View {
        SpawnAgentView(viewModel: viewModel, broadcaster: broadcaster)
    }
}
