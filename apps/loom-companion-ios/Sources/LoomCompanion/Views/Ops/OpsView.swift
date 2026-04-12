import SwiftUI
import LoomCompanionKit

struct OpsView: View {
    @State private var viewModel: OpsViewModel
    @State private var selectedSegment: OpsSegment = .work
    @Binding private var deepLinkWorkflowID: String?
    @Binding private var prefillEndSessionID: String?
    private var broadcaster: SSEEventBroadcaster?
    @State private var deepLinkedWorkflow: MobileWorkflow?
    @State private var pendingDeepLinkWorkflowID: String?
    @State private var toastMessage: String?
    @State private var showToast = false

    enum OpsSegment: String, CaseIterable, Identifiable {
        case work = "Queue"
        case pipelines = "Pipelines"
        case agents = "Runtime"
        case knowledge = "Context"

        var id: String { rawValue }
    }

    init(
        apiClient: APIClient?,
        broadcaster: SSEEventBroadcaster? = nil,
        deepLinkWorkflowID: Binding<String?> = .constant(nil),
        prefillEndSessionID: Binding<String?> = .constant(nil)
    ) {
        let client = apiClient ?? APIClient(baseURL: URL(string: "http://localhost")!, token: "mock-token")
        self.broadcaster = broadcaster
        _deepLinkWorkflowID = deepLinkWorkflowID
        _prefillEndSessionID = prefillEndSessionID
        _viewModel = State(initialValue: OpsViewModel(apiClient: client))
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                Picker("Ops Segment", selection: $selectedSegment) {
                    ForEach(OpsSegment.allCases) { segment in
                        Text(segment.rawValue).tag(segment)
                    }
                }
                .pickerStyle(.segmented)
                .onChange(of: selectedSegment) { _, _ in
                    HapticManager.light()
                }

                if let error = viewModel.error {
                    Text(error.description)
                        .font(.subheadline)
                        .foregroundStyle(LoomColors.statusCritical)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if let warningMessage = viewModel.warningMessage {
                    Label(warningMessage, systemImage: "exclamationmark.circle")
                        .font(.footnote)
                        .foregroundStyle(LoomColors.textSecondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 10)
                        .background(LoomColors.statusInfo.opacity(0.08), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                }
                if let statusMessage = viewModel.mutationStatusMessage {
                    Text(statusMessage)
                        .font(.footnote)
                        .foregroundStyle(LoomColors.fgSecondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if let mutationErrorMessage = viewModel.mutationErrorMessage {
                    Text(mutationErrorMessage)
                        .font(.footnote)
                        .foregroundStyle(LoomColors.statusCritical)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }

                if viewModel.isLoading {
                    VStack(spacing: LoomSpacing.cardSpacing) {
                        SkeletonDashboardCard()
                            .cardAppear(index: 0)
                        SkeletonDashboardCard()
                            .cardAppear(index: 1)
                        SkeletonDashboardCard()
                            .cardAppear(index: 2)
                    }
                }

                switch selectedSegment {
                case .work:
                    OpsWorkSection(
                        viewModel: viewModel,
                        prefillEndSession: { sessionID in
                            prefillEndSession(with: sessionID)
                        }
                    )
                case .pipelines:
                    OpsPipelinesSection(viewModel: viewModel)
                case .agents:
                    OpsRuntimeSection(viewModel: viewModel, broadcaster: broadcaster)
                case .knowledge:
                    OpsContextSection(viewModel: viewModel)
                }
            }
            .padding()
        }
        .navigationTitle("Work")
        .task {
            // Load only the initially selected section eagerly; others load lazily.
            await viewModel.loadSectionIfNeeded(.work)
            resolveDeepLinkWorkflow()
            if let broadcaster {
                viewModel.startListening(broadcaster: broadcaster)
            }
        }
        .refreshable {
            // On pull-to-refresh, reload the current section.
            switch selectedSegment {
            case .work:
                viewModel.loadedSections.remove(.work)
                await viewModel.loadWorkSection()
            case .pipelines:
                viewModel.loadedSections.remove(.pipelines)
                await viewModel.loadPipelinesSection()
            case .agents:
                viewModel.loadedSections.remove(.runtime)
                await viewModel.loadRuntimeSection()
            case .knowledge:
                viewModel.loadedSections.remove(.context)
                await viewModel.loadContextSection()
            }
            resolveDeepLinkWorkflow()
            HapticManager.light()
        }
        .onChange(of: deepLinkWorkflowID) { _, newValue in
            pendingDeepLinkWorkflowID = newValue
            resolveDeepLinkWorkflow()
        }
        .onChange(of: viewModel.workflows.map(\.id)) { _, _ in
            resolveDeepLinkWorkflow()
        }
        .onChange(of: prefillEndSessionID) { _, newValue in
            guard let newValue else { return }
            prefillEndSession(with: newValue)
            prefillEndSessionID = nil
        }
        .sheet(item: $deepLinkedWorkflow) { workflow in
            NavigationStack {
                OpsWorkflowDetailView(
                    workflow: workflow,
                    loadDetail: viewModel.loadWorkflowDetail(id:)
                )
            }
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

    // MARK: - Deep Link & Prefill Helpers

    private func resolveDeepLinkWorkflow() {
        let requested = pendingDeepLinkWorkflowID ?? deepLinkWorkflowID
        guard let workflowID = requested?.trimmingCharacters(in: .whitespacesAndNewlines),
              !workflowID.isEmpty
        else {
            return
        }

        if let workflow = viewModel.workflows.first(where: { $0.id == workflowID }) {
            selectedSegment = .work
            deepLinkedWorkflow = workflow
            pendingDeepLinkWorkflowID = nil
            deepLinkWorkflowID = nil
            return
        }

        guard !viewModel.isLoading else { return }

        pendingDeepLinkWorkflowID = nil
        deepLinkWorkflowID = nil
        showToastMessage("Workflow \(workflowID) is not in the current list")
    }

    private func prefillEndSession(with sessionID: String) {
        let trimmed = sessionID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        selectedSegment = .work
        showToastMessage("End Session prefilled: \(trimmed)")
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
