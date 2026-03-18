import SwiftUI
import WidgetKit
import LoomCompanionKit

/// Shows the most recently completed session: agent name, namespace, duration, token count.
struct SessionSummaryWidget: Widget {
    let kind = "SessionSummaryWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: SessionSummaryProvider()) { entry in
            SessionSummaryWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Last Session")
        .description("Summary of the most recently completed coding session.")
        .supportedFamilies([.systemSmall])
    }
}

struct SessionSummaryEntry: WidgetKit.TimelineEntry {
    let date: Date
    let session: CompletedSessionWidgetData?
}

struct SessionSummaryProvider: TimelineProvider {
    func placeholder(in context: Context) -> SessionSummaryEntry {
        SessionSummaryEntry(date: .now, session: CompletedSessionWidgetData(
            agentId: "claude-code",
            agentType: "claude-code",
            namespace: "loom-core/feature",
            durationSeconds: 1234,
            tokenCount: 4500,
            entryCount: 42,
            endedAt: ISO8601DateFormatter().string(from: .now)
        ))
    }

    func getSnapshot(in context: Context, completion: @escaping (SessionSummaryEntry) -> Void) {
        let data = SharedDataStore.load()?.lastCompletedSession
        completion(SessionSummaryEntry(date: .now, session: data))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<SessionSummaryEntry>) -> Void) {
        let data = SharedDataStore.load()?.lastCompletedSession
        let entry = SessionSummaryEntry(date: .now, session: data)
        let nextUpdate = Calendar.current.date(byAdding: .minute, value: 30, to: .now) ?? .now
        completion(Timeline(entries: [entry], policy: .after(nextUpdate)))
    }
}

struct SessionSummaryWidgetView: View {
    let entry: SessionSummaryEntry

    var body: some View {
        if let session = entry.session {
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Image(systemName: agentIcon(session.agentType))
                        .foregroundStyle(agentColor(session.agentType))
                        .font(.title3)
                    Spacer()
                    Text("Last")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }

                Spacer()

                Text(session.agentId)
                    .font(.subheadline)
                    .fontWeight(.semibold)
                    .lineLimit(1)

                Text(session.namespace)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                HStack(spacing: 10) {
                    Label(formatDuration(session.durationSeconds), systemImage: "clock")
                        .font(.caption2)
                        .foregroundStyle(.secondary)

                    if session.tokenCount > 0 {
                        Label(formatTokens(session.tokenCount), systemImage: "textformat.abc")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        } else {
            VStack(spacing: 8) {
                Image(systemName: "clock.arrow.circlepath")
                    .font(.title2)
                    .foregroundStyle(.secondary)
                Text("No recent sessions")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func agentIcon(_ type: String) -> String {
        switch type.lowercased() {
        case "claude-code", "claude": return "terminal.fill"
        case "gemini": return "wand.and.sparkles"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        default: return "cpu.fill"
        }
    }

    private func agentColor(_ type: String) -> Color {
        switch type.lowercased() {
        case "claude-code", "claude": return Color(red: 0.85, green: 0.55, blue: 0.25)
        case "gemini": return Color(red: 0.3, green: 0.65, blue: 0.95)
        case "codex": return Color(red: 0.4, green: 0.8, blue: 0.4)
        default: return .indigo
        }
    }

    private func formatDuration(_ seconds: Int) -> String {
        let minutes = seconds / 60
        if minutes >= 60 {
            return "\(minutes / 60)h \(minutes % 60)m"
        }
        return "\(minutes)m"
    }

    private func formatTokens(_ count: Int) -> String {
        if count >= 1000 {
            return String(format: "%.1fk", Double(count) / 1000.0)
        }
        return "\(count)"
    }
}
