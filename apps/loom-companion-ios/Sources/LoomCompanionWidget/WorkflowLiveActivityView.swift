import SwiftUI
import WidgetKit
import ActivityKit
import LoomCompanionKit

@available(iOS 16.2, *)
struct WorkflowLiveActivityView: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: WorkflowActivityAttributes.self) { context in
            lockScreenView(context: context)
                .activityBackgroundTint(.black.opacity(0.7))
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Image(systemName: statusIcon(context.state.status))
                        .foregroundStyle(statusColor(context.state.status))
                        .font(.title2)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    VStack(alignment: .trailing) {
                        Text("Step \(context.state.currentStepIndex + 1)/\(context.state.totalSteps)")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        Text(formattedElapsed(context.state.elapsedSeconds))
                            .font(.caption)
                            .fontDesign(.monospaced)
                    }
                }
                DynamicIslandExpandedRegion(.center) {
                    VStack(spacing: 4) {
                        Text(context.attributes.workflowName)
                            .font(.headline)
                            .lineLimit(1)
                        Text(context.state.currentStepName)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                DynamicIslandExpandedRegion(.bottom) {
                    ProgressView(value: context.state.progress)
                        .tint(statusColor(context.state.status))
                        .padding(.horizontal)
                }
            } compactLeading: {
                Image(systemName: statusIcon(context.state.status))
                    .foregroundStyle(statusColor(context.state.status))
            } compactTrailing: {
                Text(context.state.currentStepName)
                    .font(.caption2)
                    .lineLimit(1)
            } minimal: {
                Image(systemName: statusIcon(context.state.status))
                    .foregroundStyle(statusColor(context.state.status))
            }
        }
    }

    @ViewBuilder
    private func lockScreenView(context: ActivityViewContext<WorkflowActivityAttributes>) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Image(systemName: statusIcon(context.state.status))
                    .foregroundStyle(statusColor(context.state.status))
                VStack(alignment: .leading, spacing: 2) {
                    Text(context.attributes.workflowName)
                        .font(.headline)
                    Text(context.attributes.agentId)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text(formattedElapsed(context.state.elapsedSeconds))
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(context.state.currentStepName)
                        .font(.subheadline)
                        .fontWeight(.medium)
                    Spacer()
                    Text("Step \(context.state.currentStepIndex + 1) of \(context.state.totalSteps)")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                ProgressView(value: context.state.progress)
                    .tint(statusColor(context.state.status))
            }

            if context.state.status == "waiting_approval" {
                HStack(spacing: 12) {
                    Link(destination: URL(string: "loom://workflow/\(context.attributes.workflowId)/approve")!) {
                        Label("Approve", systemImage: "checkmark.circle")
                            .font(.caption)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(.green.opacity(0.2))
                            .clipShape(Capsule())
                    }
                    Link(destination: URL(string: "loom://workflow/\(context.attributes.workflowId)")!) {
                        Label("View", systemImage: "arrow.right.circle")
                            .font(.caption)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(.blue.opacity(0.2))
                            .clipShape(Capsule())
                    }
                }
            }
        }
        .padding()
    }

    private func statusIcon(_ status: String) -> String {
        switch status {
        case "running": return "play.circle.fill"
        case "waiting_approval": return "clock.badge.questionmark"
        case "completed": return "checkmark.circle.fill"
        case "failed": return "xmark.circle.fill"
        case "cancelled": return "slash.circle.fill"
        default: return "circle.dotted"
        }
    }

    private func statusColor(_ status: String) -> Color {
        switch status {
        case "running": return .blue
        case "waiting_approval": return .orange
        case "completed": return .green
        case "failed": return .red
        case "cancelled": return .gray
        default: return .secondary
        }
    }

    private func formattedElapsed(_ seconds: Int) -> String {
        let minutes = seconds / 60
        let secs = seconds % 60
        if minutes > 0 {
            return "\(minutes)m \(secs)s"
        }
        return "\(secs)s"
    }
}
