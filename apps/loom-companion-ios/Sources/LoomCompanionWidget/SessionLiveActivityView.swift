import SwiftUI
import WidgetKit
import ActivityKit
import LoomCompanionKit

@available(iOS 16.2, *)
struct SessionLiveActivityView: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: SessionActivityAttributes.self) { context in
            lockScreenView(context: context)
                .activityBackgroundTint(.black.opacity(0.7))
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    VStack(alignment: .leading, spacing: 2) {
                        Image(systemName: agentIcon(context.attributes.agentType))
                            .foregroundStyle(agentColor(context.attributes.agentType))
                            .font(.title2)
                        Text(context.attributes.agentId)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                DynamicIslandExpandedRegion(.trailing) {
                    VStack(alignment: .trailing, spacing: 2) {
                        Text(context.attributes.startDate, style: .timer)
                            .font(.system(.caption, design: .monospaced))
                            .multilineTextAlignment(.trailing)
                        if context.state.tokenCount > 0 {
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
                Image(systemName: statusDot(context.state.status))
                    .foregroundStyle(statusDotColor(context.state.status))
                    .symbolEffect(.pulse, options: .repeating, value: context.state.status == "active")
            }
        }
    }

    @ViewBuilder
    private func lockScreenView(context: ActivityViewContext<SessionActivityAttributes>) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            // Header: agent info + timer
            HStack {
                Image(systemName: agentIcon(context.attributes.agentType))
                    .foregroundStyle(agentColor(context.attributes.agentType))
                    .font(.title3)
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
                Spacer()
                statusBadge(context.state.status)
            }
        }
        .padding()
    }

    @ViewBuilder
    private func statusBadge(_ status: String) -> some View {
        Text(status.capitalized)
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(statusDotColor(status).opacity(0.2))
            .foregroundStyle(statusDotColor(status))
            .clipShape(Capsule())
    }

    private func agentIcon(_ agentType: String) -> String {
        switch agentType.lowercased() {
        case "claude-code", "claude": return "terminal.fill"
        case "gemini": return "wand.and.sparkles"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        case "kilocode": return "ruler.fill"
        case "antigravity": return "arrow.up.circle.fill"
        default: return "cpu.fill"
        }
    }

    private func agentColor(_ agentType: String) -> Color {
        switch agentType.lowercased() {
        case "claude-code", "claude": return Color(red: 0.85, green: 0.55, blue: 0.25)
        case "gemini": return Color(red: 0.3, green: 0.65, blue: 0.95)
        case "codex": return Color(red: 0.4, green: 0.8, blue: 0.4)
        case "kilocode": return Color(red: 0.7, green: 0.4, blue: 0.9)
        case "antigravity": return Color(red: 0.95, green: 0.4, blue: 0.4)
        default: return .indigo
        }
    }

    private func statusDot(_ status: String) -> String {
        switch status {
        case "active": return "circle.fill"
        case "idle": return "circle.dotted"
        case "ended", "summarized": return "checkmark.circle.fill"
        default: return "circle"
        }
    }

    private func statusDotColor(_ status: String) -> Color {
        switch status {
        case "active": return .green
        case "idle": return .orange
        case "ended": return .gray
        case "summarized": return .blue
        default: return .secondary
        }
    }

    private func formatTokens(_ count: Int) -> String {
        if count >= 1000 {
            return String(format: "%.1fk", Double(count) / 1000.0)
        }
        return "\(count)"
    }
}
