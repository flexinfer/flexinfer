import SwiftUI
import LoomCompanionKit

/// Work section: tasks, legacy workflows/approvals, and session controls.
struct OpsWorkSection: View {
    @Bindable var viewModel: OpsViewModel
    var broadcaster: SSEEventBroadcaster?
    @State private var taskDisplayLimit = 8
    @State private var workflowDisplayLimit = 8
    @State private var showLegacyWorkflows = false
    @State private var showSessionControls = false

    @State private var createAgentID = ""
    @State private var createProject: String = ""
    @State private var createNamespaceOverride = ""
    @State private var useCustomNamespace = false
    @State private var createDescription = ""
    @State private var createAutoRecall = true
    @State private var endSessionID = ""
    @State private var endWithSummary = false
    @State private var showCreateConfirmation = false
    @State private var showEndConfirmation = false

    var prefillEndSession: (String) -> Void = { _ in }

    var body: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            tasksCard
                .cardAppear(index: 0)

            if viewModel.pendingApprovals > 0 || !viewModel.workflows.isEmpty {
                legacyApprovalsCard
                    .cardAppear(index: 1)
            }

            sessionControlsCard
        }
        .task {
            await viewModel.loadSectionIfNeeded(.work)
        }
        .confirmationDialog("Start Session?", isPresented: $showCreateConfirmation, titleVisibility: .visible) {
            Button("Start Session") {
                Task {
                    await viewModel.createSession(
                        agentID: createAgentID,
                        namespace: resolvedCreateNamespace,
                        description: createDescription,
                        autoRecall: createAutoRecall
                    )
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This binds a new agent-context session to an agent that is already running on your fleet.")
        }
        .task {
            await viewModel.loadSpawnConfig()
        }
        .confirmationDialog("End Session?", isPresented: $showEndConfirmation, titleVisibility: .visible) {
            Button("End Session", role: .destructive) {
                Task { await viewModel.endSession(sessionID: endSessionID, summarize: endWithSummary) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(endWithSummary ? "This will end the session and request a summary." : "This will end the session without summary.")
        }
    }

    /// Accept an external prefill for the end-session field.
    func prefillEndSessionID(_ sessionID: String) {
        let trimmed = sessionID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        endSessionID = trimmed
    }

    // MARK: - Tasks Card

    private var tasksCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Primary Work")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                Text("Queue keeps the active worklist up front. Runtime tools and context stay one step away instead of competing for the same space.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)

                HStack {
                    opsMetric(label: "Pending", value: viewModel.taskCounts.pending, icon: "clock", color: LoomColors.statusIdle)
                    Spacer()
                    opsMetric(label: "Active", value: viewModel.taskCounts.inProgress, icon: "bolt.fill", color: LoomColors.statusActive)
                    Spacer()
                    opsMetric(label: "Blocked", value: viewModel.taskCounts.blocked, icon: "exclamationmark.triangle.fill", color: LoomColors.statusBlocked)
                    Spacer()
                    opsMetric(label: "Done", value: viewModel.taskCounts.completed, icon: "checkmark.circle.fill", color: LoomColors.statusHealthy)
                }

                if viewModel.tasks.isEmpty {
                    Text("No tasks")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textTertiary)
                } else {
                    ForEach(Array(viewModel.tasks.prefix(taskDisplayLimit))) { task in
                        NavigationLink {
                            OpsTaskDetailView(task: task)
                        } label: {
                            HStack(spacing: LoomSpacing.sm) {
                                StatusAccentBar(color: taskStatusColor(task.status))
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text(task.title)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                            .lineLimit(1)
                                        StatusBadge(
                                            task.status.rawValue,
                                            color: taskStatusColor(task.status)
                                        )
                                    }
                                    HStack(spacing: 6) {
                                        Text(task.agentId.isEmpty ? "Unknown agent" : task.agentId)
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                        Text("\u{2022}")
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textTertiary)
                                        Text(task.priority)
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                        if let sourceLabel = task.sourceLabel {
                                            StatusBadge(
                                                sourceLabel,
                                                color: LoomColors.statusInfo
                                            )
                                        }
                                    }
                                    .lineLimit(1)
                                    if let linkage = task.linkageSummary {
                                        Text(linkage)
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textTertiary)
                                            .lineLimit(1)
                                    }
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                            }
                            .padding(.vertical, 2)
                        }
                        .contextMenu {
                            Button {
                                prefillEndSession(task.sessionId)
                            } label: {
                                Label("Prefill End Session", systemImage: "arrowshape.turn.up.left")
                            }
                            .disabled(task.sessionId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }
                    }
                    if viewModel.tasks.count > taskDisplayLimit {
                        Button {
                            withAnimation(.easeInOut(duration: 0.25)) {
                                taskDisplayLimit += 8
                            }
                            HapticManager.light()
                        } label: {
                            Text("Show \(min(8, viewModel.tasks.count - taskDisplayLimit)) More")
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.accent)
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 6)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Legacy Approvals Card

    private var legacyApprovalsCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                HStack(spacing: LoomSpacing.xs) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(LoomColors.statusDegraded)
                    Text("Legacy approvals")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textSecondary)
                    Spacer()
                    if viewModel.pendingApprovals > 0 {
                        AnimatedCounter(viewModel.pendingApprovals)
                            .font(LoomTypography.counterMedium)
                            .foregroundStyle(LoomColors.statusDegraded)
                    }
                }

                Text(viewModel.workflowsDeprecationMessage ?? "Deprecated approvals only. Use tasks and pipelines for active work.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)

                DisclosureGroup(isExpanded: $showLegacyWorkflows) {
                    VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                        HStack {
                            Text("Approvals in queue")
                                .font(LoomTypography.bodyRegular)
                                .foregroundStyle(LoomColors.textTertiary)
                            Spacer()
                            AnimatedCounter(viewModel.pendingApprovals)
                                .font(LoomTypography.counterMedium)
                                .foregroundStyle(viewModel.pendingApprovals > 0 ? LoomColors.statusDegraded : LoomColors.textTertiary)
                        }

                        if viewModel.workflows.isEmpty {
                            Text("No legacy workflows")
                                .font(LoomTypography.bodyRegular)
                                .foregroundStyle(LoomColors.textTertiary)
                        } else {
                            ForEach(Array(viewModel.workflows.prefix(workflowDisplayLimit))) { workflow in
                                NavigationLink {
                                    OpsWorkflowDetailView(
                                        workflow: workflow,
                                        loadDetail: viewModel.loadWorkflowDetail(id:)
                                    )
                                } label: {
                                    HStack(spacing: LoomSpacing.sm) {
                                        StatusAccentBar(color: workflowStatusColor(workflow.status))
                                        VStack(alignment: .leading, spacing: 2) {
                                            HStack(spacing: 6) {
                                                Text(workflow.name ?? workflow.id)
                                                    .font(LoomTypography.bodyMedium)
                                                    .foregroundStyle(LoomColors.textSecondary)
                                                    .lineLimit(1)
                                                StatusBadge(
                                                    workflow.status.rawValue,
                                                    color: workflowStatusColor(workflow.status)
                                                )
                                            }
                                            Text(workflow.currentStep ?? "No current step")
                                                .font(LoomTypography.caption)
                                                .foregroundStyle(LoomColors.textTertiary)
                                        }
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                    }
                                    .padding(.vertical, 2)
                                }
                            }
                            if viewModel.workflows.count > workflowDisplayLimit {
                                Button {
                                    withAnimation(.easeInOut(duration: 0.25)) {
                                        workflowDisplayLimit += 8
                                    }
                                    HapticManager.light()
                                } label: {
                                    Text("Show \(min(8, viewModel.workflows.count - workflowDisplayLimit)) More")
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.accent)
                                        .frame(maxWidth: .infinity)
                                        .padding(.vertical, 6)
                                }
                            }
                        }
                    }
                } label: {
                    HStack {
                        Text(showLegacyWorkflows ? "Hide legacy approvals" : "Show legacy approvals")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.accent)
                        Spacer()
                        Image(systemName: showLegacyWorkflows ? "chevron.up" : "chevron.down")
                            .font(.caption2)
                            .foregroundStyle(LoomColors.accent)
                    }
                    .contentShape(Rectangle())
                }
            }
        }
    }

    // MARK: - Session Controls Card

    private var sessionControlsCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                DisclosureGroup(isExpanded: $showSessionControls) {
                    VStack(alignment: .leading, spacing: LoomSpacing.md) {
                        spawnCTA

                        Divider()

                        startSessionForm

                        Divider()

                        endSessionForm
                    }
                } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Session controls")
                                .font(LoomTypography.headlineMedium)
                                .foregroundStyle(LoomColors.textPrimary)
                            Text("Attach a session to an agent that is already running, or end a stale one.")
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.textTertiary)
                        }
                        Spacer()
                        Image(systemName: showSessionControls ? "chevron.up" : "chevron.down")
                            .font(.caption2)
                            .foregroundStyle(LoomColors.accent)
                    }
                    .contentShape(Rectangle())
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var spawnCTA: some View {
        NavigationLink {
            SpawnAgentView(
                viewModel: SpawnViewModel(apiClient: viewModel.apiClient),
                broadcaster: broadcaster
            )
        } label: {
            HStack(alignment: .center, spacing: LoomSpacing.sm) {
                Image(systemName: "sparkles")
                    .foregroundStyle(LoomColors.accent)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Spawn a new agent")
                        .font(LoomTypography.bodyMedium)
                        .foregroundStyle(LoomColors.textPrimary)
                    Text("Launch a headless runtime with a task. Use this when you don't already have an agent to attach to.")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                        .multilineTextAlignment(.leading)
                }
                Spacer()
                Image(systemName: "chevron.right")
                    .font(.caption)
                    .foregroundStyle(LoomColors.accent)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private var startSessionForm: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            Text("Start Session")
                .font(LoomTypography.bodyMedium)
            Text("Binds a session to an agent-context server so a live agent can record work against it.")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textTertiary)

            TextField("Agent ID (e.g. claude-code-…)", text: $createAgentID)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.never)
                #endif

            projectPicker

            TextField("Description (optional)", text: $createDescription)
                .textFieldStyle(.roundedBorder)
            Toggle("Auto recall", isOn: $createAutoRecall)

            Button {
                viewModel.clearMutationMessages()
                showCreateConfirmation = true
            } label: {
                if viewModel.isMutatingSession {
                    ProgressView()
                        .frame(maxWidth: .infinity)
                } else {
                    Text("Start Session")
                        .frame(maxWidth: .infinity)
                }
            }
            .buttonStyle(.borderedProminent)
            .disabled(!canStartSession)

            if let hint = startSessionDisabledHint {
                Text(hint)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)
            }
        }
    }

    @ViewBuilder
    private var projectPicker: some View {
        if let projects = viewModel.spawnConfig?.projects, !projects.isEmpty {
            Menu {
                Button("No namespace") {
                    createProject = ""
                    useCustomNamespace = false
                }
                ForEach(projects) { project in
                    Button(project.name) {
                        createProject = project.name
                        useCustomNamespace = false
                    }
                }
                Divider()
                Button("Custom namespace…") {
                    useCustomNamespace = true
                }
            } label: {
                HStack {
                    Image(systemName: "folder")
                        .foregroundStyle(LoomColors.accent)
                    Text(projectPickerLabel)
                        .foregroundStyle(LoomColors.textPrimary)
                    Spacer()
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.caption2)
                        .foregroundStyle(LoomColors.textTertiary)
                }
                .padding(.horizontal, LoomSpacing.sm)
                .padding(.vertical, 10)
                .background(LoomColors.bgElevated, in: RoundedRectangle(cornerRadius: 8))
            }

            if useCustomNamespace {
                TextField("Namespace", text: $createNamespaceOverride)
                    .textFieldStyle(.roundedBorder)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
            }
        } else {
            TextField("Namespace (optional)", text: $createNamespaceOverride)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.never)
                #endif
        }
    }

    private var projectPickerLabel: String {
        if useCustomNamespace {
            return "Custom: \(createNamespaceOverride.isEmpty ? "enter namespace" : createNamespaceOverride)"
        }
        return createProject.isEmpty ? "Select project" : createProject
    }

    private var endSessionForm: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            Text("End Session")
                .font(LoomTypography.bodyMedium)
            TextField("Session ID", text: $endSessionID)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.never)
                #endif
            Toggle("Include summary", isOn: $endWithSummary)

            Button(role: .destructive) {
                viewModel.clearMutationMessages()
                showEndConfirmation = true
            } label: {
                if viewModel.isMutatingSession {
                    ProgressView()
                        .frame(maxWidth: .infinity)
                } else {
                    Text("End Session")
                        .frame(maxWidth: .infinity)
                }
            }
            .buttonStyle(.bordered)
            .disabled(endSessionID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || viewModel.isMutatingSession)
        }
    }

    private var resolvedCreateNamespace: String {
        if useCustomNamespace {
            return createNamespaceOverride
        }
        return createProject
    }

    private var canStartSession: Bool {
        !createAgentID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !viewModel.isMutatingSession
    }

    private var startSessionDisabledHint: String? {
        if viewModel.isMutatingSession { return nil }
        if createAgentID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return "Enter an Agent ID to start a session."
        }
        return nil
    }

    // MARK: - Helpers

    private func opsMetric(label: String, value: Int, icon: String, color: Color) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.caption2)
                    .foregroundStyle(color)
                AnimatedCounter(value)
                    .font(LoomTypography.counterSmall)
                    .foregroundStyle(LoomColors.textPrimary)
            }
            Text(label)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        }
    }

    private func taskStatusColor(_ status: MobileTaskStatus) -> Color {
        switch status {
        case .pending: return LoomColors.statusIdle
        case .inProgress: return LoomColors.statusActive
        case .blocked: return LoomColors.statusBlocked
        case .completed: return LoomColors.statusHealthy
        case .unknown: return LoomColors.statusIdle
        }
    }

    private func workflowStatusColor(_ status: MobileWorkflowStatus) -> Color {
        switch status {
        case .running: return LoomColors.statusActive
        case .completed: return LoomColors.statusHealthy
        case .failed: return LoomColors.statusCritical
        case .waitingApproval: return LoomColors.statusDegraded
        case .cancelled: return LoomColors.statusIdle
        case .unknown: return LoomColors.statusIdle
        }
    }
}
