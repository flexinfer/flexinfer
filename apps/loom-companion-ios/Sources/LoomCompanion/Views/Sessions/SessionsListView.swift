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
    private let embeddedInPeopleTab: Bool

    init(
        apiClient: APIClient?,
        deepLinkSessionID: Binding<String?> = .constant(nil),
        embeddedInPeopleTab: Bool = false,
        onPrefillEndSession: ((String) -> Void)? = nil
    ) {
        let client: any LoomAPIClientProtocol = apiClient ?? NoOpClient()
        self.apiClient = client
        _deepLinkSessionID = deepLinkSessionID
        self.embeddedInPeopleTab = embeddedInPeopleTab
        self.onPrefillEndSession = onPrefillEndSession
        _viewModel = State(initialValue: SessionsViewModel(apiClient: client))
    }

    var body: some View {
        NavigationStack(path: $navigationPath) {
            List {
                if embeddedInPeopleTab {
                    EmbeddedSessionSearchField(text: $viewModel.searchText, prompt: "Search sessions")
                }

                SessionFilterView(
                    statusFilter: $viewModel.statusFilter,
                    agentFilter: $viewModel.agentFilter,
                    namespaceFilter: $viewModel.namespaceFilter,
                    availableAgents: viewModel.availableAgents,
                    availableNamespaces: viewModel.uniqueNamespaces
                )

                ForEach(viewModel.filteredSessionRows) { row in
                    NavigationLink(value: row.session.id) {
                        SessionRowView(
                            session: row.session,
                            depth: row.depth,
                            childCount: row.childCount,
                            activeChildCount: row.activeChildCount,
                            isOrphan: row.isOrphan
                        )
                    }
                    .swipeActions(edge: .trailing) {
                        Button(role: .destructive) {
                            HapticManager.medium()
                            onPrefillEndSession?(row.session.id)
                        } label: {
                            Label("End", systemImage: "stop.circle")
                        }
                        .tint(LoomColors.statusCritical)
                    }
                    .swipeActions(edge: .leading) {
                        Button {
                            HapticManager.light()
                            navigationPath.append(row.session.id)
                        } label: {
                            Label("View", systemImage: "eye")
                        }
                        .tint(LoomColors.accent)
                    }
                    .contextMenu {
                        Button {
                            onPrefillEndSession?(row.session.id)
                        } label: {
                            Label("Prefill End in Ops", systemImage: "arrowshape.turn.up.left")
                        }
                    }
                }
            }
            .navigationTitle(embeddedInPeopleTab ? "" : "Sessions")
            .navigationBarTitleDisplayMode(.inline)
            .navigationDestination(for: String.self) { sessionId in
                SessionDetailView(sessionId: sessionId, apiClient: apiClient)
            }
            .modifier(EmbeddedSessionNavigationChrome(isHidden: embeddedInPeopleTab))
            .modifier(SessionSearchableWhenStandalone(
                isEnabled: !embeddedInPeopleTab,
                text: $viewModel.searchText,
                prompt: "Search sessions"
            ))
            .safeAreaInset(edge: .bottom) {
                Color.clear
                    .frame(height: 96)
                    .allowsHitTesting(false)
            }
            .refreshable {
                await viewModel.load()
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
                    ScrollView {
                        LoomEmptyState(
                            tone: .idle,
                            title: "No sessions yet",
                            detail: "Agent sessions appear here when coding agents connect.\nStart from the Work tab or launch an agent from your terminal."
                        ) {
                            if !embeddedInPeopleTab {
                                Button {
                                    showingCreateSheet = true
                                } label: {
                                    Label("Create session", systemImage: "plus.circle")
                                        .font(LoomTypography.labelLarge)
                                }
                                .buttonStyle(.borderedProminent)
                                .tint(LoomColors.accent)
                            }
                        }
                    }
                } else if viewModel.filteredSessions.isEmpty && !viewModel.sessions.isEmpty {
                    ScrollView {
                        LoomEmptyState(
                            tone: .attention,
                            title: "No matching sessions",
                            detail: "Filter excluded \(viewModel.sessions.count) session\(viewModel.sessions.count == 1 ? "" : "s"). Adjust filters or clear search."
                        )
                    }
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

private struct EmbeddedSessionSearchField: View {
    @Binding var text: String
    let prompt: String

    var body: some View {
        HStack(spacing: LoomSpacing.sm) {
            Image(systemName: "magnifyingglass")
                .font(.body)
                .foregroundStyle(LoomColors.textSecondary)
            TextField(prompt, text: $text)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
        }
        .padding(.horizontal, LoomSpacing.md)
        .padding(.vertical, 10)
        .background(LoomColors.bgElevated, in: Capsule())
        .listRowInsets(EdgeInsets(top: 4, leading: 16, bottom: 6, trailing: 16))
        .listRowBackground(Color.clear)
    }
}

private struct EmbeddedSessionNavigationChrome: ViewModifier {
    let isHidden: Bool

    @ViewBuilder
    func body(content: Content) -> some View {
        if isHidden {
            content.toolbar(.hidden, for: .navigationBar)
        } else {
            content
        }
    }
}

private struct SessionSearchableWhenStandalone: ViewModifier {
    let isEnabled: Bool
    @Binding var text: String
    let prompt: String

    @ViewBuilder
    func body(content: Content) -> some View {
        if isEnabled {
            content.searchable(text: $text, prompt: Text(prompt))
        } else {
            content
        }
    }
}
