import SwiftUI
import LoomCompanionKit

struct SessionDetailView: View {
    let sessionId: String
    @State private var viewModel: SessionDetailViewModel
    @State private var showingEndConfirmation = false
    @State private var showingEndError = false
    @State private var entriesExpanded = true
    @State private var eventsExpanded = true
    @State private var liveActivityStarted = false

    init(sessionId: String, apiClient: any LoomAPIClientProtocol) {
        self.sessionId = sessionId
        _viewModel = State(initialValue: SessionDetailViewModel(apiClient: apiClient))
    }

    private var isActive: Bool {
        viewModel.session?.status == .active
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if let session = viewModel.session {
                    SessionMetadataView(session: session)
                        .cardAppear(index: 0)

                    // Tasks summary
                    if let tasks = viewModel.tasks, tasks.total > 0 {
                        SessionTasksView(tasks: tasks)
                            .cardAppear(index: 1)
                    }

                    // Context entry breakdown (collapsible)
                    if !viewModel.entryBreakdown.isEmpty {
                        DisclosureGroup(isExpanded: $entriesExpanded) {
                            SessionEntryBreakdownView(buckets: viewModel.entryBreakdown)
                        } label: {
                            Label("Entry Breakdown", systemImage: "chart.bar")
                                .font(.headline)
                        }
                        .animation(.spring(duration: 0.35), value: entriesExpanded)
                        .cardAppear(index: 2)
                    }

                    // Decisions
                    if !viewModel.decisions.isEmpty {
                        SessionEntriesSection(title: "Decisions", icon: "lightbulb", entries: viewModel.decisions)
                            .cardAppear(index: 3)
                    }

                    // Errors
                    if !viewModel.errors.isEmpty {
                        SessionEntriesSection(title: "Errors", icon: "exclamationmark.triangle", entries: viewModel.errors)
                            .cardAppear(index: 4)
                    }

                    // Top files
                    if !viewModel.topFiles.isEmpty {
                        SessionTopFilesView(files: viewModel.topFiles)
                            .cardAppear(index: 5)
                    }

                    // Events timeline (collapsible)
                    DisclosureGroup(isExpanded: $eventsExpanded) {
                        SessionEventsView(events: viewModel.events)
                    } label: {
                        Label("Events (\(viewModel.events.count))", systemImage: "clock.arrow.circlepath")
                            .font(.headline)
                            .contentTransition(.numericText())
                    }
                    .animation(.spring(duration: 0.35), value: eventsExpanded)
                    .cardAppear(index: 6)
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
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                if isActive {
                    #if os(iOS)
                    if #available(iOS 16.2, *) {
                        Button {
                            HapticManager.light()
                            startLiveActivity()
                        } label: {
                            Label(
                                liveActivityStarted ? "Activity Running" : "Live Activity",
                                systemImage: liveActivityStarted
                                    ? "dot.radiowaves.left.and.right"
                                    : "dot.radiowaves.left.and.right"
                            )
                            .symbolEffect(.pulse, isActive: liveActivityStarted)
                        }
                        .tint(liveActivityStarted ? .green : nil)
                    }
                    #endif

                    if viewModel.isEnding {
                        ProgressView()
                    } else {
                        Button("End Session", role: .destructive) {
                            showingEndConfirmation = true
                        }
                    }
                }
            }
        }
        .confirmationDialog(
            "End Session?",
            isPresented: $showingEndConfirmation,
            titleVisibility: .visible
        ) {
            Button("End Session", role: .destructive) {
                Task { await viewModel.endSession(summarize: false) }
            }
            Button("End with Summary", role: .destructive) {
                Task { await viewModel.endSession(summarize: true) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This will terminate the session. You can optionally generate a summary.")
        }
        .alert("Failed to End Session", isPresented: $showingEndError) {
            Button("OK") { viewModel.endError = nil }
        } message: {
            Text(viewModel.endError ?? "Unknown error")
        }
        .onChange(of: viewModel.endError) { _, newValue in
            showingEndError = newValue != nil
        }
        .refreshable {
            await viewModel.load(sessionId: sessionId)
        }
        .task {
            await viewModel.load(sessionId: sessionId)
            updateLiveActivityState()
        }
    }

    private func updateLiveActivityState() {
        #if os(iOS)
        if #available(iOS 16.2, *) {
            liveActivityStarted = LiveActivityManager.shared.hasSessionActivity(sessionId: sessionId)
        }
        #endif
    }

    #if os(iOS)
    @available(iOS 16.2, *)
    private func startLiveActivity() {
        guard let session = viewModel.session else { return }

        let agentType: String
        let agentId = session.agentId
        let lowered = agentId.lowercased()
        if lowered.contains("claude") { agentType = "claude-code" }
        else if lowered.contains("gemini") { agentType = "gemini" }
        else if lowered.contains("codex") { agentType = "codex" }
        else { agentType = "unknown" }

        LiveActivityManager.shared.startSessionActivity(
            sessionId: session.id,
            agentId: agentId,
            agentType: agentType,
            namespace: session.namespace
        )

        withAnimation(.spring(duration: 0.3)) {
            liveActivityStarted = true
        }
        HapticManager.success()
    }
    #endif
}
