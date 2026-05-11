import SwiftUI
import LoomCompanionKit

/// Work tab — Queue is primary, everything else is a collapsible peek.
///
/// The previous layout used a 4-way segmented picker (Queue / Pipelines /
/// Runtime / Context) that treated all four as equals. In practice, Queue is
/// the critical path — pending tasks, blockers, approvals. Pipelines, Runtime,
/// and Context are supporting context: useful when triaging, noise otherwise.
///
/// New layout:
///   1. Queue section renders at the top of the scroll, always visible.
///   2. Three DisclosureGroups below (Pipelines, Runtime, Context) with a
///      summary line — expand inline to peek without losing Queue context.
///   3. Sections load lazily on first expand + on pull-to-refresh when open.
struct OpsView: View {
    @State private var viewModel: OpsViewModel
    @Binding private var deepLinkWorkflowID: String?
    @Binding private var taskFilter: NavigationCoordinator.TasksFilter?
    @Binding private var prefillEndSessionID: String?
    private var broadcaster: SSEEventBroadcaster?

    @State private var deepLinkedWorkflow: MobileWorkflow?
    @State private var pendingDeepLinkWorkflowID: String?
    @State private var toastMessage: String?
    @State private var showToast = false

    @State private var expandedPipelines = false
    @State private var expandedRuntime = false
    @State private var expandedContext = false

    init(
        apiClient: APIClient?,
        broadcaster: SSEEventBroadcaster? = nil,
        deepLinkWorkflowID: Binding<String?> = .constant(nil),
        taskFilter: Binding<NavigationCoordinator.TasksFilter?> = .constant(nil),
        prefillEndSessionID: Binding<String?> = .constant(nil)
    ) {
        let client = apiClient ?? APIClient(baseURL: URL(string: "http://localhost")!, token: "mock-token")
        self.broadcaster = broadcaster
        _deepLinkWorkflowID = deepLinkWorkflowID
        _taskFilter = taskFilter
        _prefillEndSessionID = prefillEndSessionID
        _viewModel = State(initialValue: OpsViewModel(apiClient: client))
    }

    var body: some View {
        ScrollView {
            VStack(spacing: LoomSpacing.md) {
                diagnostics

                // Queue — primary, full-weight
                queueHero

                // Peek disclosures — supporting context
                peekDisclosure(
                    title: "Pipelines",
                    icon: "arrow.triangle.2.circlepath",
                    color: pipelinesAccent,
                    summary: pipelinesSummary,
                    isExpanded: $expandedPipelines,
                    onExpandLoad: { await viewModel.loadSectionIfNeeded(.pipelines) }
                ) {
                    OpsPipelinesSection(viewModel: viewModel)
                }

                peekDisclosure(
                    title: "Runtime",
                    icon: "cpu",
                    color: runtimeAccent,
                    summary: runtimeSummary,
                    isExpanded: $expandedRuntime,
                    onExpandLoad: { await viewModel.loadSectionIfNeeded(.runtime) }
                ) {
                    OpsRuntimeSection(viewModel: viewModel, broadcaster: broadcaster)
                }

                peekDisclosure(
                    title: "Context",
                    icon: "brain",
                    color: LoomColors.tierShortTerm,
                    summary: contextSummary,
                    isExpanded: $expandedContext,
                    onExpandLoad: { await viewModel.loadSectionIfNeeded(.context) }
                ) {
                    OpsContextSection(viewModel: viewModel)
                }
            }
            .padding()
            .animation(.spring(duration: 0.35, bounce: 0.18), value: expandedPipelines)
            .animation(.spring(duration: 0.35, bounce: 0.18), value: expandedRuntime)
            .animation(.spring(duration: 0.35, bounce: 0.18), value: expandedContext)
        }
        .navigationTitle("Work")
        .task {
            await viewModel.loadSectionIfNeeded(.work)
            resolveDeepLinkWorkflow()
            if let broadcaster {
                viewModel.startListening(broadcaster: broadcaster)
            }
        }
        .refreshable {
            // Always refresh the Queue (primary).
            viewModel.loadedSections.remove(.work)
            await viewModel.loadWorkSection()
            // Refresh any expanded panels so the peek stays in sync.
            if expandedPipelines {
                viewModel.loadedSections.remove(.pipelines)
                await viewModel.loadPipelinesSection()
            }
            if expandedRuntime {
                viewModel.loadedSections.remove(.runtime)
                await viewModel.loadRuntimeSection()
            }
            if expandedContext {
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
                    .padding(.horizontal, LoomSpacing.md)
                    .padding(.vertical, LoomSpacing.sm)
                    .background(Color.black.opacity(0.85))
                    .clipShape(Capsule())
                    .padding(.top, LoomSpacing.sm)
                    .transition(.opacity)
            }
        }
    }

    // MARK: - Diagnostics banners (error / warning / mutation status)

    @ViewBuilder
    private var diagnostics: some View {
        if let error = viewModel.error {
            Text(error.description)
                .font(LoomTypography.bodyRegular)
                .foregroundStyle(LoomColors.statusCritical)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        if let warning = viewModel.warningMessage {
            Label(warning, systemImage: "exclamationmark.circle")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(LoomSpacing.md)
                .background(
                    LoomColors.statusInfo.opacity(0.08),
                    in: RoundedRectangle(cornerRadius: 14, style: .continuous)
                )
        }
        if let status = viewModel.mutationStatusMessage {
            Text(status)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.fgSecondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        if let mutationError = viewModel.mutationErrorMessage {
            Text(mutationError)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.statusCritical)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    // MARK: - Queue hero

    @ViewBuilder
    private var queueHero: some View {
        if viewModel.isLoading && viewModel.tasks.isEmpty && viewModel.workflows.isEmpty {
            // Initial load skeleton — just the queue, no peek placeholders
            SkeletonDashboardCard()
                .cardAppear(index: 0)
        } else {
            OpsWorkSection(
                viewModel: viewModel,
                taskFilter: taskFilter,
                clearTaskFilter: { taskFilter = nil },
                prefillEndSession: { sessionID in
                    prefillEndSession(with: sessionID)
                }
            )
        }
    }

    // MARK: - Peek disclosure

    /// Collapsible panel that lazily loads its content on first expand.
    /// Header reads like a LoomListRow summary for visual consistency.
    @ViewBuilder
    private func peekDisclosure<Content: View>(
        title: String,
        icon: String,
        color: Color,
        summary: String,
        isExpanded: Binding<Bool>,
        onExpandLoad: @escaping () async -> Void,
        @ViewBuilder content: @escaping () -> Content
    ) -> some View {
        LoomCard(priority: .compact) {
            VStack(alignment: .leading, spacing: 0) {
                Button {
                    HapticManager.selection()
                    isExpanded.wrappedValue.toggle()
                    if isExpanded.wrappedValue {
                        Task { await onExpandLoad() }
                    }
                } label: {
                    peekHeader(title: title, icon: icon, color: color, summary: summary, isExpanded: isExpanded.wrappedValue)
                }
                .buttonStyle(.plain)

                if isExpanded.wrappedValue {
                    Divider()
                        .overlay(LoomColors.border)
                        .padding(.vertical, LoomSpacing.sm)
                    content()
                        .transition(
                            .asymmetric(
                                insertion: .opacity.combined(with: .move(edge: .top)),
                                removal: .opacity
                            )
                        )
                }
            }
        }
    }

    private func peekHeader(title: String, icon: String, color: Color, summary: String, isExpanded: Bool) -> some View {
        HStack(spacing: LoomSpacing.sm) {
            LoomRowIcon(systemName: icon, color: color, size: 12)

            VStack(alignment: .leading, spacing: 1) {
                Text(title)
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(LoomColors.fgPrimary)
                Text(summary)
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.fgMuted)
                    .lineLimit(1)
            }

            Spacer()

            Image(systemName: "chevron.down")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(LoomColors.fgMuted)
                .rotationEffect(.degrees(isExpanded ? 0 : -90))
                .animation(.spring(duration: 0.25), value: isExpanded)
        }
        .contentShape(Rectangle())
    }

    // MARK: - Summaries (collapsed state)

    private var pipelinesSummary: String {
        if !viewModel.pipelinesAvailable && viewModel.loadedSections.contains(.pipelines) {
            return "not available"
        }
        let summary = viewModel.pipelineSummary
        let running = summary?.running ?? 0
        let failed = summary?.failed ?? 0
        let total = viewModel.pipelines.count
        if total == 0 { return "no pipelines" }
        var parts: [String] = []
        if running > 0 { parts.append("\(running) running") }
        if failed > 0 { parts.append("\(failed) failed") }
        if parts.isEmpty { parts.append("\(total) idle") }
        return parts.joined(separator: " · ")
    }

    private var pipelinesAccent: Color {
        let failed = viewModel.pipelineSummary?.failed ?? 0
        let running = viewModel.pipelineSummary?.running ?? 0
        if failed > 0 { return LoomColors.statusCritical }
        if running > 0 { return LoomColors.statusActive }
        return LoomColors.fgMuted
    }

    private var runtimeSummary: String {
        let active = viewModel.presenceSummary.activeAgents
        let idle = viewModel.presenceSummary.idleAgents
        let offline = viewModel.presenceSummary.offlineAgents
        let claims = viewModel.presenceSummary.claimCount
        if active == 0 && idle == 0 && offline == 0 {
            return "no agents"
        }
        var parts: [String] = ["\(active) active"]
        if idle > 0 { parts.append("\(idle) idle") }
        if offline > 0 { parts.append("\(offline) offline") }
        if claims > 0 { parts.append("\(claims) claims") }
        return parts.joined(separator: " · ")
    }

    private var runtimeAccent: Color {
        let active = viewModel.presenceSummary.activeAgents
        let offline = viewModel.presenceSummary.offlineAgents
        if offline > 0 && active == 0 { return LoomColors.statusDegraded }
        if active > 0 { return LoomColors.statusHealthy }
        return LoomColors.fgMuted
    }

    private var contextSummary: String {
        guard let stats = viewModel.memoryStats else {
            return viewModel.loadedSections.contains(.context) ? "no memory" : "tap to load"
        }
        let working = stats.workingMemory.items
        let shortTerm = stats.shortTermMemory.items
        let longTerm = stats.longTermMemory.items
        return "\(working) working · \(shortTerm) short · \(longTerm) long"
    }

    // MARK: - Deep link & prefill

    private func resolveDeepLinkWorkflow() {
        let requested = pendingDeepLinkWorkflowID ?? deepLinkWorkflowID
        guard let workflowID = requested?.trimmingCharacters(in: .whitespacesAndNewlines),
              !workflowID.isEmpty
        else {
            return
        }

        if let workflow = viewModel.workflows.first(where: { $0.id == workflowID }) {
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
