import SwiftUI
import LoomCompanionKit

struct SessionsListView: View {
    @State private var viewModel: SessionsViewModel
    @State private var showingCreateSheet = false
    @State private var navigationPath: [String] = []
    @State private var pendingDeepLinkSessionID: String?
    @State private var toastMessage: String?
    @State private var showToast = false
    @Binding private var deepLinkSessionID: String?
    private let onPrefillEndSession: ((String) -> Void)?
    private let apiClient: any LoomAPIClientProtocol

    init(
        apiClient: APIClient?,
        deepLinkSessionID: Binding<String?> = .constant(nil),
        onPrefillEndSession: ((String) -> Void)? = nil
    ) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpClient()
        self.apiClient = client
        _deepLinkSessionID = deepLinkSessionID
        self.onPrefillEndSession = onPrefillEndSession
        _viewModel = State(initialValue: SessionsViewModel(apiClient: client))
    }

    var body: some View {
        NavigationStack(path: $navigationPath) {
            List {
                SessionFilterView(
                    statusFilter: $viewModel.statusFilter,
                    agentFilter: $viewModel.agentFilter,
                    namespaceFilter: $viewModel.namespaceFilter,
                    availableAgents: viewModel.availableAgents,
                    availableNamespaces: viewModel.uniqueNamespaces
                )

                ForEach(viewModel.filteredSessions) { session in
                    NavigationLink(value: session.id) {
                        SessionRowView(session: session)
                    }
                    .swipeActions(edge: .trailing) {
                        Button(role: .destructive) {
                            HapticManager.medium()
                            onPrefillEndSession?(session.id)
                        } label: {
                            Label("End", systemImage: "stop.circle")
                        }
                        .tint(LoomColors.statusCritical)
                    }
                    .swipeActions(edge: .leading) {
                        Button {
                            HapticManager.light()
                            navigationPath.append(session.id)
                        } label: {
                            Label("View", systemImage: "eye")
                        }
                        .tint(LoomColors.accent)
                    }
                    .contextMenu {
                        Button {
                            onPrefillEndSession?(session.id)
                        } label: {
                            Label("Prefill End in Ops", systemImage: "arrowshape.turn.up.left")
                        }
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
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        showingCreateSheet = true
                    } label: {
                        Label("New Session", systemImage: "plus")
                    }
                }
            }
            .sheet(isPresented: $showingCreateSheet) {
                Task { await viewModel.load() }
            } content: {
                CreateSessionView(viewModel: viewModel)
            }
            .overlay {
                if viewModel.isLoading && viewModel.sessions.isEmpty {
                    VStack(spacing: LoomSpacing.sm) {
                        ForEach(0..<5, id: \.self) { i in
                            SkeletonSessionRow()
                                .cardAppear(index: i)
                        }
                    }
                    .padding()
                } else if let error = viewModel.error, viewModel.sessions.isEmpty {
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
                } else if viewModel.sessions.isEmpty && !viewModel.isLoading {
                    ContentUnavailableView {
                        Label("No Sessions", systemImage: "person.2.circle")
                    } description: {
                        Text("Agent sessions appear here when coding agents connect. Start a session from the Ops tab or launch an agent from your terminal.")
                    } actions: {
                        Button {
                            showingCreateSheet = true
                        } label: {
                            Label("Create Session", systemImage: "plus.circle")
                        }
                        .buttonStyle(.borderedProminent)
                    }
                } else if viewModel.filteredSessions.isEmpty && !viewModel.sessions.isEmpty {
                    ContentUnavailableView.search(text: viewModel.searchText)
                }
            }
            .task {
                await viewModel.load()
                pendingDeepLinkSessionID = deepLinkSessionID
                resolveSessionDeepLink()
            }
            .onChange(of: deepLinkSessionID) { _, newValue in
                pendingDeepLinkSessionID = newValue
                resolveSessionDeepLink()
            }
            .onChange(of: viewModel.sessions.map(\.id)) { _, _ in
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

        if viewModel.sessions.contains(where: { $0.id == requested }) {
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

private struct NoOpClient: LoomAPIClientProtocol {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        throw LoomAPIError.noToken
    }
}
