import SwiftUI
import LoomCompanionKit

/// Detail view for an agent, showing session context breakdown, decisions, errors, and tasks.
struct AgentDetailView: View {
    let agent: UnifiedAgent
    @State private var viewModel: SessionDetailViewModel

    init(agent: UnifiedAgent, apiClient: any LoomAPIClientProtocol) {
        self.agent = agent
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
        .task { await loadSession() }
    }

    private func loadSession() async {
        if let sessionId = agent.sessionId, !sessionId.isEmpty {
            await viewModel.load(sessionId: sessionId)
        }
    }

    // MARK: - Sections

    private var agentHeader: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
            HStack {
                StatusBadge(presenceStatus: agent.status)
                Text(agent.agentType)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textSecondary)
                Spacer()
                if agent.needsAttention {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
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
                            .background(Color.orange.opacity(0.15))
                            .foregroundStyle(.orange)
                            .clipShape(Capsule())
                    }
                }
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
                taskPill(label: "Pending", value: tasks.pending, color: .gray)
                taskPill(label: "Active", value: tasks.inProgress, color: LoomColors.accent)
                taskPill(label: "Done", value: tasks.completed, color: .green)
            }
            if agent.blockedTasks > 0 {
                HStack(spacing: 4) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.caption)
                        .foregroundStyle(.orange)
                    Text("\(agent.blockedTasks) blocked")
                        .font(LoomTypography.caption)
                        .foregroundStyle(.orange)
                }
            }
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
                        .foregroundStyle(.red)
                        .font(.system(size: 10))
                    Text(entry.title)
                        .font(LoomTypography.caption)
                        .foregroundStyle(.red)
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
