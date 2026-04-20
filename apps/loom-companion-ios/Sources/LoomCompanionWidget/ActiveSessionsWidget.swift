import SwiftUI
import WidgetKit
import LoomCompanionKit

struct ActiveSessionsWidget: Widget {
    let kind = "ActiveSessionsWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: ActiveSessionsProvider()) { entry in
            ActiveSessionsWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
                .widgetURL(URL(string: "loom://people"))
        }
        .configurationDisplayName("Active Sessions")
        .description("See which agents are currently active.")
        .supportedFamilies([.systemMedium, .systemLarge])
    }
}

struct ActiveSessionsEntry: WidgetKit.TimelineEntry {
    let date: Date
    let data: SessionWidgetData
}

struct ActiveSessionsProvider: TimelineProvider {
    func placeholder(in context: Context) -> ActiveSessionsEntry {
        ActiveSessionsEntry(date: .now, data: SharedDataStore.placeholder.sessions)
    }

    func getSnapshot(in context: Context, completion: @escaping (ActiveSessionsEntry) -> Void) {
        let data = SharedDataStore.load()?.sessions ?? SharedDataStore.placeholder.sessions
        completion(ActiveSessionsEntry(date: .now, data: data))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<ActiveSessionsEntry>) -> Void) {
        let data = SharedDataStore.load()?.sessions ?? SharedDataStore.placeholder.sessions
        let entry = ActiveSessionsEntry(date: .now, data: data)
        let nextUpdate = Calendar.current.date(byAdding: .minute, value: 15, to: .now) ?? .now
        completion(Timeline(entries: [entry], policy: .after(nextUpdate)))
    }
}

struct ActiveSessionsWidgetView: View {
    let entry: ActiveSessionsEntry
    @Environment(\.widgetFamily) var family

    private var rowLimit: Int {
        family == .systemLarge ? 8 : 3
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // Header
            HStack {
                Image(systemName: "person.2.fill")
                    .foregroundStyle(.green)
                Text("Active Sessions")
                    .font(.headline)
                Spacer()
                Text("\(entry.data.activeCount)")
                    .font(.system(size: 22, weight: .bold, design: .rounded))
                    .foregroundStyle(.green)
                    .contentTransition(.numericText())
            }

            if entry.data.topSessions.isEmpty {
                Spacer()
                HStack {
                    Spacer()
                    VStack(spacing: 4) {
                        Image(systemName: "moon.zzz")
                            .font(.title3)
                            .foregroundStyle(.secondary)
                        Text("No active sessions")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                }
                Spacer()
            } else {
                ForEach(entry.data.topSessions.prefix(rowLimit)) { session in
                    sessionRow(session)
                }
                if entry.data.topSessions.count > rowLimit {
                    Text("+\(entry.data.topSessions.count - rowLimit) more")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .padding(.top, 2)
                }
                Spacer(minLength: 0)
            }
        }
    }

    private func sessionRow(_ session: SessionWidgetEntry) -> some View {
        let agentColor = agentTypeColor(session.agentType.isEmpty
            ? inferType(session.agentId)
            : session.agentType)

        return HStack(spacing: 8) {
            // Agent icon with status dot
            ZStack(alignment: .bottomTrailing) {
                Image(systemName: agentTypeIcon(session.agentType.isEmpty
                    ? inferType(session.agentId)
                    : session.agentType))
                    .font(.caption)
                    .foregroundStyle(agentColor)
                    .frame(width: 20, height: 20)

                // Pulsing dot for recently-active sessions
                Circle()
                    .fill(session.isRecentlyActive ? .green : .gray.opacity(0.5))
                    .frame(width: 6, height: 6)
                    .overlay(
                        session.isRecentlyActive
                            ? Circle()
                                .stroke(.green.opacity(0.4), lineWidth: 2)
                                .frame(width: 10, height: 10)
                            : nil
                    )
                    .offset(x: 2, y: 2)
            }

            VStack(alignment: .leading, spacing: 1) {
                Text(session.agentId)
                    .font(.caption)
                    .fontWeight(.medium)
                    .foregroundStyle(agentColor)
                Text(session.namespace)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            Text(session.startedAt)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .padding(.vertical, 3)
        .padding(.horizontal, 6)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(agentColor.opacity(0.06))
        )
    }

    // MARK: - Agent Type Helpers (widget can't import main app design system)

    private func agentTypeColor(_ type: String) -> Color {
        switch type.lowercased() {
        case "claude-code", "claude": return Color(red: 0.85, green: 0.55, blue: 0.25)
        case "gemini": return Color(red: 0.3, green: 0.65, blue: 0.95)
        case "codex": return Color(red: 0.4, green: 0.8, blue: 0.4)
        case "kilocode": return Color(red: 0.7, green: 0.4, blue: 0.9)
        case "antigravity": return Color(red: 0.95, green: 0.4, blue: 0.4)
        default: return .indigo
        }
    }

    private func agentTypeIcon(_ type: String) -> String {
        switch type.lowercased() {
        case "claude-code", "claude": return "terminal.fill"
        case "gemini": return "wand.and.sparkles"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        case "kilocode": return "ruler.fill"
        case "antigravity": return "arrow.up.circle.fill"
        default: return "cpu.fill"
        }
    }

    private func inferType(_ agentId: String) -> String {
        let id = agentId.lowercased()
        if id.contains("claude") { return "claude-code" }
        if id.contains("gemini") { return "gemini" }
        if id.contains("codex") { return "codex" }
        if id.contains("kilo") { return "kilocode" }
        return "unknown"
    }
}
