import SwiftUI
import LoomCompanionKit

struct TimelineListView: View {
    let entries: [TimelineEntry]

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.cardSpacing) {
                Text("Recent Activity")
                    .font(LoomTypography.headlineMedium)

                if entries.isEmpty {
                    Text("No recent events")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textSecondary)
                        .padding(.vertical, LoomSpacing.sm)
                } else {
                    ForEach(Array(entries.enumerated()), id: \.element.id) { index, entry in
                        TimelineRow(entry: entry)
                            .cardAppear(index: index)
                        if entry.id != entries.last?.id {
                            Divider()
                        }
                    }
                }
            }
        }
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
                .symbolEffect(.bounce, value: entry.id)

            VStack(alignment: .leading, spacing: 2) {
                Text(formatEventType(entry.eventType))
                    .font(LoomTypography.bodyMedium)

                if let agentId = entry.agentId {
                    Text(agentId)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                }

                Text(entry.timestamp)
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.textTertiary)
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
        case "agent.session.start": return LoomColors.statusHealthy
        case "agent.session.end": return LoomColors.statusDegraded
        case "agent.session.reaped": return LoomColors.statusCritical
        case "agent.heartbeat": return .pink
        default: return LoomColors.statusActive
        }
    }

    private func formatEventType(_ type: String) -> String {
        type.replacingOccurrences(of: ".", with: " ")
            .split(separator: " ")
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
    }
}
