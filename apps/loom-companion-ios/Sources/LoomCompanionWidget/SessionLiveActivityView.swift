import SwiftUI
import WidgetKit
import ActivityKit
import LoomCompanionKit

@available(iOS 16.2, *)
struct SessionLiveActivityView: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: SessionActivityAttributes.self) { context in
            lockScreenView(context: context)
                .activityBackgroundTint(isErrorStatus(context.state.status) ? .red.opacity(0.15) : .black.opacity(0.7))
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    VStack(alignment: .leading, spacing: 2) {
                        if isErrorStatus(context.state.status) {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .foregroundStyle(.red)
                                .font(.title2)
                        } else {
                            Image(systemName: agentIcon(context.attributes.agentType))
                                .foregroundStyle(agentColor(context.attributes.agentType))
                                .font(.title2)
                        }
                        Text(context.attributes.agentId)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                DynamicIslandExpandedRegion(.trailing) {
                    VStack(alignment: .trailing, spacing: 2) {
                        if isErrorStatus(context.state.status) {
                            Text(context.state.status == "error" ? "Error" : "Failed")
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.red)
                                .multilineTextAlignment(.trailing)
                        } else {
                            Text(context.attributes.startDate, style: .timer)
                                .font(.system(.caption, design: .monospaced))
                                .multilineTextAlignment(.trailing)
                        }
                        if context.state.estimatedCost > 0 {
                            Text(String(format: "$%.3f", context.state.estimatedCost))
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        } else if context.state.tokenCount > 0 {
                            Text("\(formatTokens(context.state.tokenCount)) tok")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                DynamicIslandExpandedRegion(.center) {
                    VStack(spacing: 4) {
                        Text(context.attributes.namespace)
                            .font(.headline)
                            .lineLimit(1)
                        if !context.state.currentTask.isEmpty {
                            Text(context.state.currentTask)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                }
                DynamicIslandExpandedRegion(.bottom) {
                    HStack(spacing: 8) {
                        if !context.state.branch.isEmpty {
                            Label(context.state.branch, systemImage: "arrow.triangle.branch")
                                .font(.caption2)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 3)
                                .background(.blue.opacity(0.2))
                                .clipShape(Capsule())
                        }
                        Spacer()
                        Text("\(context.state.entryCount) entries")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    .padding(.horizontal)
                }
            } compactLeading: {
                Image(systemName: agentIcon(context.attributes.agentType))
                    .foregroundStyle(agentColor(context.attributes.agentType))
            } compactTrailing: {
                Text(context.attributes.startDate, style: .timer)
                    .font(.system(.caption2, design: .monospaced))
                    .frame(minWidth: 36)
            } minimal: {
                if isErrorStatus(context.state.status) {
                    Image(systemName: "exclamationmark.circle.fill")
                        .foregroundStyle(.red)
                } else {
                    Image(systemName: agentIcon(context.attributes.agentType))
                        .foregroundStyle(agentColor(context.attributes.agentType))
                        .font(.caption2)
                        .symbolEffect(.pulse, options: .repeating, value: context.state.status == "active")
                }
            }
        }
    }

    @ViewBuilder
    private func lockScreenView(context: ActivityViewContext<SessionActivityAttributes>) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            // Header: agent info + timer
            HStack {
                if isErrorStatus(context.state.status) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.title3)
                } else {
                    Image(systemName: agentIcon(context.attributes.agentType))
                        .foregroundStyle(agentColor(context.attributes.agentType))
                        .font(.title3)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(context.attributes.agentId)
                        .font(.headline)
                    Text(context.attributes.namespace)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text(context.attributes.startDate, style: .timer)
                    .font(.system(.title3, design: .monospaced))
                    .foregroundStyle(.secondary)
            }

            // Branch pill + current task
            HStack(spacing: 8) {
                if !context.state.branch.isEmpty {
                    Label(context.state.branch, systemImage: "arrow.triangle.branch")
                        .font(.caption)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(.blue.opacity(0.2))
                        .clipShape(Capsule())
                }
                if !context.state.currentTask.isEmpty {
                    Text(context.state.currentTask)
                        .font(.subheadline)
                        .fontWeight(.medium)
                        .lineLimit(2)
                }
            }

            // Stats row
            HStack(spacing: 16) {
                if context.state.tokenCount > 0 {
                    Label(formatTokens(context.state.tokenCount), systemImage: "textformat.abc")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if context.state.entryCount > 0 {
                    Label("\(context.state.entryCount)", systemImage: "list.bullet")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if context.state.estimatedCost > 0 {
                    Label(String(format: "$%.3f", context.state.estimatedCost), systemImage: "dollarsign.circle")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                statusBadge(context.state.status)
            }
        }
        .padding()
    }

    @ViewBuilder
    private func statusBadge(_ status: String) -> some View {
        let label = isErrorStatus(status) ? (status == "error" ? "Error" : "Failed") : status.capitalized
        Text(label)
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(statusDotColor(status).opacity(0.2))
            .foregroundStyle(statusDotColor(status))
            .clipShape(Capsule())
    }

    private func isErrorStatus(_ status: String) -> Bool {
        status == "error" || status == "failed"
    }
}
