import SwiftUI
import LoomCompanionKit

struct AgentsListView: View {
    @State private var viewModel: AgentsViewModel
    @State private var showingCreateSheet = false
    @State private var navigationPath: [String] = []
    @State private var pendingDeepLinkSessionID: String?
    @State private var toastMessage: String?
    @State private var showToast = false
    @Binding private var deepLinkSessionID: String?
    private let onPrefillEndSession: ((String) -> Void)?
    private let apiClient: any LoomAPIClientProtocol
    private let broadcaster: SSEEventBroadcaster?
    private let embeddedInPeopleTab: Bool

    init(
        apiClient: APIClient?,
        broadcaster: SSEEventBroadcaster? = nil,
        deepLinkSessionID: Binding<String?> = .constant(nil),
        embeddedInPeopleTab: Bool = false,
        onPrefillEndSession: ((String) -> Void)? = nil
    ) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpAgentsClient()
        self.apiClient = client
        self.broadcaster = broadcaster
        _deepLinkSessionID = deepLinkSessionID
        self.embeddedInPeopleTab = embeddedInPeopleTab
        self.onPrefillEndSession = onPrefillEndSession
        _viewModel = State(initialValue: AgentsViewModel(apiClient: client))
    }

    var body: some View {
        NavigationStack(path: $navigationPath) {
            List {
                AgentFilterView(
                    statusFilter: $viewModel.statusFilter,
                    summary: viewModel.summary,
                    pipelineAgentCount: viewModel.agents.filter { $0.pipelineCount > 0 }.count
                )

                ForEach(viewModel.filteredAgents) { agent in
                    if agent.hasSession, let sessionId = agent.sessionId {
                        NavigationLink(value: sessionId) {
                            AgentRowView(agent: agent)
                        }
                        .swipeActions(edge: .trailing) {
                            if agent.sessionStatus == "active" {
                                Button(role: .destructive) {
                                    HapticManager.medium()
                                    onPrefillEndSession?(sessionId)
                                } label: {
                                    Label("End Session", systemImage: "stop.circle")
                                }
                                .tint(LoomColors.statusCritical)
                            }
                        }
                    } else {
                        AgentRowView(agent: agent)
                    }
                }
            }
            .navigationTitle(embeddedInPeopleTab ? "" : "Agents")
            .navigationBarTitleDisplayMode(.inline)
            .navigationDestination(for: String.self) { sessionId in
                SessionDetailView(sessionId: sessionId, apiClient: apiClient)
            }
            .searchable(text: $viewModel.searchText, prompt: "Search agents")
            .refreshable {
                await viewModel.load()
                HapticManager.light()
            }
            .toolbar {
                if !embeddedInPeopleTab {
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            showingCreateSheet = true
                        } label: {
                            Label("New Session", systemImage: "plus")
                        }
                    }
                }
            }
            .sheet(isPresented: $showingCreateSheet) {
                Task { await viewModel.load() }
            } content: {
                CreateSessionView(viewModel: SessionsViewModel(apiClient: apiClient))
            }
            .overlay {
                if viewModel.isLoading && viewModel.agents.isEmpty {
                    VStack(spacing: LoomSpacing.sm) {
                        ForEach(0..<5, id: \.self) { i in
                            SkeletonAgentRow()
                                .cardAppear(index: i)
                        }
                    }
                    .padding()
                } else if let error = viewModel.error, viewModel.agents.isEmpty {
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
                } else if viewModel.agents.isEmpty && !viewModel.isLoading {
                    ContentUnavailableView {
                        Label("No Agents", systemImage: "person.2.wave.2")
                    } description: {
                        Text("Agents appear here when coding agents connect via presence or sessions. Start an agent from your terminal or spawn one from the Work tab.")
                    } actions: {
                        if !embeddedInPeopleTab {
                            Button {
                                showingCreateSheet = true
                            } label: {
                                Label("Create Session", systemImage: "plus.circle")
                            }
                            .buttonStyle(.borderedProminent)
                        }
                    }
                } else if viewModel.filteredAgents.isEmpty && !viewModel.agents.isEmpty {
                    ContentUnavailableView.search(text: viewModel.searchText)
                }
            }
            .task {
                await viewModel.load()
                if let broadcaster {
                    viewModel.startListening(broadcaster: broadcaster)
                }
                pendingDeepLinkSessionID = deepLinkSessionID
                resolveSessionDeepLink()
            }
            .onChange(of: deepLinkSessionID) { _, newValue in
                pendingDeepLinkSessionID = newValue
                resolveSessionDeepLink()
            }
            .overlay(alignment: .top) {
                if showToast, let toastMessage {
                    Text(toastMessage)
                        .font(.caption)
                        .foregroundStyle(.white)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 8)
                        .background(Color.black.opacity(0.85))
                        .clipShape(Capsule())
                        .padding(.top, 8)
                        .transition(.opacity)
                }
            }
        }
    }

    private func resolveSessionDeepLink() {
        guard let requested = pendingDeepLinkSessionID?.trimmingCharacters(in: .whitespacesAndNewlines),
              !requested.isEmpty
        else { return }

        if viewModel.agents.contains(where: { $0.sessionId == requested }) {
            navigationPath.append(requested)
            pendingDeepLinkSessionID = nil
            deepLinkSessionID = nil
            return
        }

        guard !viewModel.isLoading else { return }

        pendingDeepLinkSessionID = nil
        deepLinkSessionID = nil
        showToastMessage("Session \(requested) is not in the current list")
    }

    private func showToastMessage(_ message: String) {
        toastMessage = message
        withAnimation {
            showToast = true
        }
        Task {
            try? await Task.sleep(for: .seconds(2.5))
            await MainActor.run {
                withAnimation {
                    showToast = false
                }
            }
        }
    }
}

private struct NoOpAgentsClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
