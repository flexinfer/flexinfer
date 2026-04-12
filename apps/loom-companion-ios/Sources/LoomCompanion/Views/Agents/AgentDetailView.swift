import SwiftUI
import LoomCompanionKit

/// Detail view for an agent, showing session context breakdown, decisions, errors, tasks, and pipelines.
struct AgentDetailView: View {
    let agent: UnifiedAgent
    @State private var viewModel: SessionDetailViewModel
    @State private var pipelines: [MobilePipeline] = []
    @Environment(\.navigationCoordinator) private var navigationCoordinator
    private let apiClient: any LoomAPIClientProtocol

    init(agent: UnifiedAgent, apiClient: any LoomAPIClientProtocol) {
        self.agent = agent
        self.apiClient = apiClient
        self._viewModel = State(initialValue: SessionDetailViewModel(apiClient: apiClient))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: LoomSpacing.md) {
                agentHeader
                if viewModel.isLoading {
                    ProgressView("Loading session detail...")
                        .frame(maxWidth: .infinity, alignment: .center)
                        .padding(.vertical, LoomSpacing.lg)
                } else if let session = viewModel.session {
                    sessionSummary(session)
                    if !viewModel.entryBreakdown.isEmpty {
                        entryBreakdownSection
                    }
                    if let tasks = viewModel.tasks, tasks.total > 0 {
                        taskSection(tasks)
                    }
                    if !pipelines.isEmpty {
                        pipelineSection
                    }
                    if !viewModel.decisions.isEmpty {
                        decisionSection
                    }
                    if !viewModel.errors.isEmpty {
                        errorSection
                    }
                    if !viewModel.topFiles.isEmpty {
                        topFilesSection
                    }
                } else if agent.sessionId == nil {
                    if !pipelines.isEmpty {
                        pipelineSection
                    }
                    Text("No active session")
                        .foregroundStyle(LoomColors.textSecondary)
                        .frame(maxWidth: .infinity, alignment: .center)
                        .padding(.vertical, LoomSpacing.lg)
                }
            }
            .padding(LoomSpacing.md)
        }
        .navigationTitle(agent.agentId)
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await loadSession()
            await loadPipelines()
        }
    }

    private func loadSession() async {
        if let sessionId = agent.sessionId, !sessionId.isEmpty {
            await viewModel.load(sessionId: sessionId)
        }
    }

    private func loadPipelines() async {
        guard !agent.branch.isEmpty else { return }
        do {
            let response: MobilePipelinesResponse = try await apiClient.request(.pipelines)
            pipelines = response.pipelines.filter { $0.ref == agent.branch }
        } catch {
            // Non-critical; pipeline section just won't appear.
        }
    }

    // MARK: - Sections

    private var agentHeader: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            HStack {
                Image(systemName: LoomColors.agentTypeIcon(agent.agentType))
                    .font(.system(size: 14))
                    .foregroundStyle(LoomColors.agentTypeColor(agent.agentType))
                StatusBadge(presenceStatus: agent.status)
                Text(agent.agentType)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textSecondary)
                Spacer()
                if agent.needsAttention {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(LoomColors.statusDegraded)
                        .font(.system(size: 14))
                }
            }
            if !agent.description.isEmpty {
                Text(agent.description)
                    .font(LoomTypography.bodyMedium)
                    .foregroundStyle(LoomColors.textPrimary)
            }
            HStack(spacing: LoomSpacing.sm) {
                if !agent.branch.isEmpty {
                    Label(agent.branch, systemImage: "arrow.triangle.branch")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
                if let ns = agent.namespace, !ns.isEmpty {
                    Label(ns, systemImage: "folder")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
            if !agent.attentionReasons.isEmpty {
                HStack(spacing: 4) {
                    ForEach(agent.attentionReasons, id: \.self) { reason in
                        Text(reason)
                            .font(.caption2)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(LoomColors.warningDim)
                            .foregroundStyle(LoomColors.statusDegraded)
                            .clipShape(Capsule())
                    }
                }
            }

            if agent.hasSession, let sessionId = agent.sessionId {
                Button {
                    HapticManager.selection()
                    navigationCoordinator?.navigateToSession(id: sessionId)
                } label: {
                    HStack(spacing: 6) {
                        Image(systemName: "rectangle.stack.person.crop")
                            .font(.system(size: 12))
                        Text("View Session")
                            .font(LoomTypography.caption)
                    }
                    .foregroundStyle(LoomColors.info)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(LoomColors.infoDim, in: Capsule())
                }
                .buttonStyle(.plain)
            }
        }
    }

    private func sessionSummary(_ session: SessionInfo) -> some View {
        HStack(spacing: LoomSpacing.md) {
            statPill(label: "Entries", value: "\(session.entryCount)")
            statPill(label: "Tokens", value: formatTokens(session.totalTokens))
            if agent.taskCount > 0 {
                statPill(label: "Tasks", value: "\(agent.taskCount)")
            }
            if agent.claimCount > 0 {
                statPill(label: "Claims", value: "\(agent.claimCount)")
            }
        }
    }

    private var entryBreakdownSection: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            Text("Context Breakdown")
                .font(LoomTypography.headlineMedium)
            ForEach(viewModel.entryBreakdown) { bucket in
                HStack {
                    Text(bucket.entryType)
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textPrimary)
                    Spacer()
                    Text("\(bucket.count)")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textSecondary)
                    Text("\(bucket.estimatedTokens) tok")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    private func taskSection(_ tasks: SessionTaskSummary) -> some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            Text("Tasks")
                .font(LoomTypography.headlineMedium)
            HStack(spacing: LoomSpacing.md) {
                taskPill(label: "Pending", value: tasks.pending, color: LoomColors.statusIdle)
                taskPill(label: "Active", value: tasks.inProgress, color: LoomColors.info)
                taskPill(label: "Done", value: tasks.completed, color: LoomColors.statusHealthy)
            }
            if agent.blockedTasks > 0 {
                HStack(spacing: 4) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.caption)
                        .foregroundStyle(LoomColors.statusDegraded)
                    Text("\(agent.blockedTasks) blocked")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.statusDegraded)
                }
            }
        }
    }

    private var pipelineSection: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            Text("Pipelines")
                .font(LoomTypography.headlineMedium)
            ForEach(pipelines) { pipeline in
                HStack(spacing: LoomSpacing.sm) {
                    StatusAccentBar(color: pipelineColor(pipeline.status))
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(pipeline.project.components(separatedBy: "/").last ?? pipeline.project)
                                .font(LoomTypography.bodyMedium)
                                .foregroundStyle(LoomColors.textPrimary)
                                .lineLimit(1)
                            StatusBadge(pipeline.status, color: pipelineColor(pipeline.status))
                        }
                        HStack(spacing: 4) {
                            Text(pipeline.ref)
                                .font(LoomTypography.monoCaption)
                                .foregroundStyle(LoomColors.accent)
                            if let stage = pipeline.currentStage {
                                Text("\u{2022} \(stage)")
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textSecondary)
                            }
                        }
                        if pipeline.totalStages > 0 {
                            ProgressView(value: Double(pipeline.completedStages), total: Double(pipeline.totalStages))
                                .tint(pipelineColor(pipeline.status))
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    if pipeline.failedJobCount > 0 {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(LoomColors.statusCritical)
                            .font(.caption)
                    }
                }
                .padding(.vertical, 2)
            }
        }
    }

    private func pipelineColor(_ status: String) -> Color {
        switch status {
        case "running": return LoomColors.statusActive
        case "success": return LoomColors.statusHealthy
        case "failed": return LoomColors.statusCritical
        case "pending": return LoomColors.statusIdle
        default: return LoomColors.textTertiary
        }
    }

    private var decisionSection: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            Text("Decisions")
                .font(LoomTypography.headlineMedium)
            ForEach(viewModel.decisions) { entry in
                VStack(alignment: .leading, spacing: 2) {
                    Text(entry.title)
                        .font(LoomTypography.bodyMedium)
                        .foregroundStyle(LoomColors.textPrimary)
                    Text(entry.timestamp)
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
                .padding(.vertical, 2)
            }
        }
    }

    private var errorSection: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            Text("Errors")
                .font(LoomTypography.headlineMedium)
            ForEach(viewModel.errors) { entry in
                HStack(spacing: 4) {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(LoomColors.statusCritical)
                        .font(.system(size: 10))
                    Text(entry.title)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.statusCritical)
                }
            }
        }
    }

    private var topFilesSection: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            Text("Top Files")
                .font(LoomTypography.headlineMedium)
            ForEach(viewModel.topFiles) { file in
                HStack {
                    Text(file.filePath)
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textPrimary)
                        .lineLimit(1)
                    Spacer()
                    Text("\(file.touchCount)")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    // MARK: - Helpers

    private func statPill(label: String, value: String) -> some View {
        VStack(spacing: 2) {
            Text(value)
                .font(LoomTypography.headlineMedium)
                .foregroundStyle(LoomColors.textPrimary)
            Text(label)
                .font(.caption2)
                .foregroundStyle(LoomColors.textTertiary)
        }
        .frame(minWidth: 50)
    }

    private func taskPill(label: String, value: Int, color: Color) -> some View {
        HStack(spacing: 3) {
            Circle()
                .fill(color)
                .frame(width: 6, height: 6)
            Text("\(value) \(label)")
                .font(.caption2)
        }
    }

    private func formatTokens(_ tokens: Int) -> String {
        if tokens >= 1000 {
            return String(format: "%.1fk", Double(tokens) / 1000.0)
        }
        return "\(tokens)"
    }
}
