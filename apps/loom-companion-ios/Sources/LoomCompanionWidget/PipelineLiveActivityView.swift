import SwiftUI
import WidgetKit
import ActivityKit
import LoomCompanionKit

@available(iOS 16.2, *)
struct PipelineLiveActivityView: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: PipelineActivityAttributes.self) { context in
            lockScreenView(context: context)
                .activityBackgroundTint(.black.opacity(0.7))
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Image(systemName: "server.rack")
                        .foregroundStyle(pipelineStatusColor(context.state.status))
                        .font(.title2)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    VStack(alignment: .trailing, spacing: 2) {
                        Text(context.state.stageFraction)
                            .font(.caption)
                            .fontDesign(.monospaced)
                        Text(context.attributes.startDate, style: .timer)
                            .font(.caption2)
                            .fontDesign(.monospaced)
                            .foregroundStyle(.secondary)
                    }
                }
                DynamicIslandExpandedRegion(.center) {
                    VStack(spacing: 4) {
                        Text(shortProject(context.attributes.project))
                            .font(.headline)
                            .lineLimit(1)
                        Text(context.state.currentStage)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                DynamicIslandExpandedRegion(.bottom) {
                    stageProgressBar(context: context)
                        .padding(.horizontal)
                }
            } compactLeading: {
                Image(systemName: pipelineStatusIcon(context.state.status))
                    .foregroundStyle(pipelineStatusColor(context.state.status))
            } compactTrailing: {
                Text(context.state.stageFraction)
                    .font(.system(.caption2, design: .monospaced))
            } minimal: {
                Image(systemName: pipelineStatusIcon(context.state.status))
                    .foregroundStyle(pipelineStatusColor(context.state.status))
            }
        }
    }

    @ViewBuilder
    private func lockScreenView(context: ActivityViewContext<PipelineActivityAttributes>) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            // Header: GitLab icon + project + ref + timer
            HStack {
                Image(systemName: "server.rack")
                    .foregroundStyle(pipelineStatusColor(context.state.status))
                    .font(.title3)
                VStack(alignment: .leading, spacing: 2) {
                    Text(shortProject(context.attributes.project))
                        .font(.headline)
                    Text(context.attributes.ref)
                        .font(.caption)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.purple.opacity(0.2))
                        .clipShape(Capsule())
                }
                Spacer()
                Text(context.attributes.startDate, style: .timer)
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
            }

            // Current stage + stage progress
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(context.state.currentStage)
                        .font(.subheadline)
                        .fontWeight(.medium)
                    Spacer()
                    Text("Stage \(context.state.completedStages)/\(context.state.totalStages)")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                stageProgressBar(context: context)
            }

            // Status row
            HStack(spacing: 12) {
                statusBadge(context.state.status)
                if context.state.failedJobCount > 0 {
                    Label("\(context.state.failedJobCount) failed", systemImage: "xmark.circle")
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                Spacer()
            }
        }
        .padding()
    }

    @ViewBuilder
    private func stageProgressBar(context: ActivityViewContext<PipelineActivityAttributes>) -> some View {
        GeometryReader { geo in
            let total = max(context.state.totalStages, 1)
            let segmentWidth = geo.size.width / CGFloat(total)
            HStack(spacing: 2) {
                ForEach(0..<total, id: \.self) { index in
                    RoundedRectangle(cornerRadius: 2)
                        .fill(stageColor(index: index, context: context))
                        .frame(width: max(segmentWidth - 2, 4), height: 6)
                }
            }
        }
        .frame(height: 6)
    }

    private func stageColor(index: Int, context: ActivityViewContext<PipelineActivityAttributes>) -> Color {
        let completed = context.state.completedStages
        if index < completed {
            return .green // passed
        } else if index == completed {
            if context.state.status == "failed" {
                return .red
            }
            return .blue // running
        }
        return Color.gray.opacity(0.3) // pending
    }

    @ViewBuilder
    private func statusBadge(_ status: String) -> some View {
        Text(pipelineStatusLabel(status))
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(pipelineStatusColor(status).opacity(0.2))
            .foregroundStyle(pipelineStatusColor(status))
            .clipShape(Capsule())
    }

    private func pipelineStatusIcon(_ status: String) -> String {
        switch status {
        case "running": return "play.circle.fill"
        case "pending": return "clock.fill"
        case "success": return "checkmark.circle.fill"
        case "failed": return "xmark.circle.fill"
        case "canceled": return "slash.circle.fill"
        default: return "circle.dotted"
        }
    }

    private func pipelineStatusColor(_ status: String) -> Color {
        switch status {
        case "running": return .blue
        case "pending": return .orange
        case "success": return .green
        case "failed": return .red
        case "canceled": return .gray
        default: return .secondary
        }
    }

    private func pipelineStatusLabel(_ status: String) -> String {
        switch status {
        case "running": return "Running"
        case "pending": return "Pending"
        case "success": return "Passed"
        case "failed": return "Failed"
        case "canceled": return "Canceled"
        default: return status.capitalized
        }
    }

    /// Extract short project name from full path (e.g., "group/project" → "project").
    private func shortProject(_ project: String) -> String {
        if let last = project.split(separator: "/").last {
            return String(last)
        }
        return project
    }
}
