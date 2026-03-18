import SwiftUI
import LoomCompanionKit

struct SessionDetailView: View {
    let sessionId: String
    @State private var viewModel: SessionDetailViewModel
    @State private var showingEndConfirmation = false
    @State private var showingEndError = false

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

                    // Tasks summary
                    if let tasks = viewModel.tasks, tasks.total > 0 {
                        SessionTasksView(tasks: tasks)
                    }

                    // Context entry breakdown
                    if !viewModel.entryBreakdown.isEmpty {
                        SessionEntryBreakdownView(buckets: viewModel.entryBreakdown)
                    }

                    // Decisions
                    if !viewModel.decisions.isEmpty {
                        SessionEntriesSection(title: "Decisions", icon: "lightbulb", entries: viewModel.decisions)
                    }

                    // Errors
                    if !viewModel.errors.isEmpty {
                        SessionEntriesSection(title: "Errors", icon: "exclamationmark.triangle", entries: viewModel.errors)
                    }

                    // Top files
                    if !viewModel.topFiles.isEmpty {
                        SessionTopFilesView(files: viewModel.topFiles)
                    }

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
        .toolbar {
            if isActive {
                ToolbarItem(placement: .primaryAction) {
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
        }
    }
}
