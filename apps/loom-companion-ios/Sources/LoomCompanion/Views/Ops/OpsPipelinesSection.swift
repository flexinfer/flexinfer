import SwiftUI
import LoomCompanionKit

/// Pipelines section: CI pipeline status, active and recent pipelines.
struct OpsPipelinesSection: View {
    @Bindable var viewModel: OpsViewModel
    @State private var pipelineDisplayLimit = 8

    var body: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            pipelinesCard
                .cardAppear(index: 0)
        }
        .task {
            await viewModel.loadSectionIfNeeded(.pipelines)
        }
    }

    // MARK: - Pipelines Card

    private var pipelinesCard: some View {
        let summary = viewModel.pipelineSummary ?? derivedPipelineSummary()
        let recentCompleted = recentCompletedPipelines()
        return LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.md) {
                HStack {
                    Image(systemName: "arrow.triangle.branch")
                        .foregroundStyle(LoomColors.accent)
                    Text("CI Pipelines")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)
                    Spacer()
                    if let lastActivity = summary.lastActivity,
                       !lastActivity.isEmpty {
                        Text("Updated \(lastActivity)")
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                }

                HStack {
                    opsMetric(label: "Running", value: summary.running, icon: "play.circle.fill", color: LoomColors.statusActive)
                    Spacer()
                    opsMetric(label: "Passed", value: summary.passed, icon: "checkmark.circle.fill", color: LoomColors.statusHealthy)
                    Spacer()
                    opsMetric(label: "Failed", value: summary.failed, icon: "xmark.circle.fill", color: LoomColors.statusCritical)
                    Spacer()
                    opsMetric(label: "Pending", value: summary.pending, icon: "clock", color: LoomColors.statusIdle)
                }

                if !viewModel.pipelinesAvailable && viewModel.pipelines.isEmpty && viewModel.recentPipelines.isEmpty {
                    VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                        Label("Pipeline monitoring is unavailable right now", systemImage: "wifi.exclamationmark")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textSecondary)
                        Text("Runtime, tasks, and sessions are still live. CI cards will repopulate automatically when pipeline data comes back.")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                } else if !viewModel.pipelines.isEmpty {
                    VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                        Text("Active Pipelines")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textTertiary)

                        ForEach(Array(viewModel.pipelines.prefix(pipelineDisplayLimit))) { pipeline in
                            pipelineRow(pipeline: pipeline, isRecent: false)
                        }

                        if viewModel.pipelines.count > pipelineDisplayLimit {
                            Button {
                                withAnimation(.easeInOut(duration: 0.25)) {
                                    pipelineDisplayLimit += 8
                                }
                                HapticManager.light()
                            } label: {
                                Text("Show \(min(8, viewModel.pipelines.count - pipelineDisplayLimit)) More")
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.accent)
                                    .frame(maxWidth: .infinity)
                                    .padding(.vertical, 6)
                            }
                        }
                    }
                } else if viewModel.recentPipelines.isEmpty {
                    VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                        Text("No pipeline activity")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                        Text("When CI runs again, the active and recent sections will populate automatically.")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                } else {
                    VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                        Text("No active pipelines")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                        Text("Recent builds still appear below when available.")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                }

                if !recentCompleted.isEmpty {
                    VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                        Text("Recent Pipelines")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textTertiary)

                        ForEach(Array(recentCompleted.prefix(5))) { pipeline in
                            pipelineRow(pipeline: pipeline, isRecent: true)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Pipeline Row

    private func pipelineRow(pipeline: MobilePipeline, isRecent: Bool) -> some View {
        HStack(spacing: LoomSpacing.sm) {
            StatusAccentBar(color: pipelineStatusColor(pipeline.status))
                .opacity(isRecent ? 0.6 : 1)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(pipeline.project.components(separatedBy: "/").last ?? pipeline.project)
                        .font(LoomTypography.bodyMedium)
                        .foregroundStyle(isRecent ? LoomColors.textSecondary : LoomColors.textPrimary)
                        .lineLimit(1)
                    StatusBadge(pipeline.status, color: pipelineStatusColor(pipeline.status))
                }
                HStack(spacing: 4) {
                    Text(pipeline.ref)
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(isRecent ? LoomColors.textTertiary : LoomColors.accent)
                    if let stage = pipeline.currentStage {
                        Text("\u{2022} \(stage)")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                    if let agentId = pipeline.agentId, !agentId.isEmpty {
                        HStack(spacing: 2) {
                            Image(systemName: LoomColors.agentTypeIcon(pipeline.agentType ?? agentId))
                                .font(.system(size: 8))
                            Text(agentId)
                        }
                        .font(.caption2)
                        .foregroundStyle(LoomColors.agentTypeColor(pipeline.agentType ?? agentId))
                    }
                }
                if let stages = pipeline.stages, !stages.isEmpty, !isRecent {
                    HStack(spacing: 4) {
                        ForEach(Array(stages.prefix(3))) { stage in
                            Text(stage.name)
                                .font(LoomTypography.labelSmall)
                                .foregroundStyle(pipelineStatusColor(stage.status))
                                .padding(.horizontal, 6)
                                .padding(.vertical, 2)
                                .background(pipelineStatusColor(stage.status).opacity(0.12), in: Capsule())
                        }
                        if stages.count > 3 {
                            Text("+\(stages.count - 3)")
                                .font(LoomTypography.labelSmall)
                                .foregroundStyle(LoomColors.textTertiary)
                        }
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            if pipeline.totalStages > 0 && !isRecent {
                Text("\(pipeline.completedStages)/\(pipeline.totalStages)")
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.textTertiary)
            }

            if pipeline.failedJobCount > 0 && !isRecent {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(LoomColors.statusCritical)
                    .font(.caption)
            }
        }
        .padding(.vertical, 2)
        .opacity(isRecent ? 0.72 : 1)
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

    private func derivedPipelineSummary() -> MobilePipelineSummary {
        var combined: [MobilePipeline] = []
        var seen = Set<Int>()
        for pipeline in viewModel.pipelines + viewModel.recentPipelines {
            if seen.insert(pipeline.id).inserted {
                combined.append(pipeline)
            }
        }

        var running = 0
        var passed = 0
        var failed = 0
        var pending = 0

        for pipeline in combined {
            switch pipeline.status.lowercased() {
            case "running":
                running += 1
            case "success", "passed":
                passed += 1
            case "pending", "created", "scheduled", "manual":
                pending += 1
            default:
                failed += 1
            }
        }

        return MobilePipelineSummary(running: running, passed: passed, failed: failed, pending: pending, lastActivity: nil)
    }

    private func recentCompletedPipelines() -> [MobilePipeline] {
        viewModel.recentPipelines.filter { pipeline in
            switch pipeline.status.lowercased() {
            case "running", "pending", "created", "scheduled", "manual":
                return false
            default:
                return true
            }
        }
    }

    private func pipelineStatusColor(_ status: String) -> Color {
        switch status {
        case "running": return LoomColors.statusActive
        case "success": return LoomColors.statusHealthy
        case "failed": return LoomColors.statusCritical
        case "pending": return LoomColors.statusIdle
        case "canceled", "cancelled": return LoomColors.statusIdle
        default: return LoomColors.textTertiary
        }
    }
}
