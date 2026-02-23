import SwiftUI
import LoomCompanionKit

struct TimelineListView: View {
    let entries: [TimelineEntry]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Recent Activity")
                .font(.headline)

            if entries.isEmpty {
                Text("No recent events")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 8)
            } else {
                ForEach(entries) { entry in
                    TimelineRow(entry: entry)
                    if entry.id != entries.last?.id {
                        Divider()
                    }
                }
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

private struct TimelineRow: View {
    let entry: TimelineEntry

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: iconForEventType(entry.eventType))
                .font(.body)
                .foregroundStyle(colorForEventType(entry.eventType))
                .frame(width: 24)

            VStack(alignment: .leading, spacing: 2) {
                Text(formatEventType(entry.eventType))
                    .font(.subheadline)
                    .fontWeight(.medium)

                if let agentId = entry.agentId {
                    Text(agentId)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Text(entry.timestamp)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }

            Spacer()
        }
    }

    private func iconForEventType(_ type: String) -> String {
        switch type {
        case "agent.session.start": return "play.circle.fill"
        case "agent.session.end": return "stop.circle.fill"
        case "agent.session.reaped": return "xmark.circle.fill"
        case "agent.heartbeat": return "heart.fill"
        case "hud.fleet": return "server.rack"
        case "hud.health": return "heart.text.clipboard"
        case "hud.task.create": return "checkmark.circle"
        case "hud.memory.add": return "brain"
        default: return "circle.fill"
        }
    }

    private func colorForEventType(_ type: String) -> Color {
        switch type {
        case "agent.session.start": return .green
        case "agent.session.end": return .orange
        case "agent.session.reaped": return .red
        case "agent.heartbeat": return .pink
        default: return .blue
        }
    }

    private func formatEventType(_ type: String) -> String {
        type.replacingOccurrences(of: ".", with: " ")
            .split(separator: " ")
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
    }
}
