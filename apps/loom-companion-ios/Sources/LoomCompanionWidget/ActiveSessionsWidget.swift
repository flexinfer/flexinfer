import SwiftUI
import WidgetKit
import LoomCompanionKit

struct ActiveSessionsWidget: Widget {
    let kind = "ActiveSessionsWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: ActiveSessionsProvider()) { entry in
            ActiveSessionsWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Active Sessions")
        .description("See which agents are currently active.")
        .supportedFamilies([.systemMedium])
    }
}

struct ActiveSessionsEntry: TimelineEntry {
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

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "person.2.fill")
                    .foregroundStyle(.green)
                Text("Active Sessions")
                    .font(.headline)
                Spacer()
                Text("\(entry.data.activeCount)")
                    .font(.system(size: 22, weight: .bold, design: .rounded))
                    .foregroundStyle(.green)
            }

            if entry.data.topSessions.isEmpty {
                Text("No active sessions")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
            } else {
                ForEach(entry.data.topSessions.prefix(3)) { session in
                    HStack(spacing: 8) {
                        RoundedRectangle(cornerRadius: 2)
                            .fill(.green)
                            .frame(width: 3)

                        VStack(alignment: .leading, spacing: 1) {
                            Text(session.agentId)
                                .font(.caption)
                                .fontWeight(.medium)
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
                }
                Spacer()
            }
        }
    }
}
