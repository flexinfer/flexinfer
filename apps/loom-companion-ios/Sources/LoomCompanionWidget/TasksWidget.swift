import SwiftUI
import WidgetKit
import LoomCompanionKit

struct TasksWidget: Widget {
    let kind = "TasksWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: TasksProvider()) { entry in
            TasksWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
                .widgetURL(URL(string: "loom://tasks"))
        }
        .configurationDisplayName("Tasks")
        .description("Track pending and blocked tasks.")
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

struct TasksEntry: WidgetKit.TimelineEntry {
    let date: Date
    let data: TaskWidgetData
}

struct TasksProvider: TimelineProvider {
    func placeholder(in context: Context) -> TasksEntry {
        TasksEntry(date: .now, data: SharedDataStore.placeholder.tasks)
    }

    func getSnapshot(in context: Context, completion: @escaping (TasksEntry) -> Void) {
        let data = SharedDataStore.load()?.tasks ?? SharedDataStore.placeholder.tasks
        completion(TasksEntry(date: .now, data: data))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<TasksEntry>) -> Void) {
        let data = SharedDataStore.load()?.tasks ?? SharedDataStore.placeholder.tasks
        let entry = TasksEntry(date: .now, data: data)
        let nextUpdate = Calendar.current.date(byAdding: .minute, value: 15, to: .now) ?? .now
        completion(Timeline(entries: [entry], policy: .after(nextUpdate)))
    }
}

struct TasksWidgetView: View {
    let entry: TasksEntry
    @Environment(\.widgetFamily) var family

    var body: some View {
        switch family {
        case .systemSmall:
            smallView
        case .systemMedium:
            mediumView
        default:
            smallView
        }
    }

    private var smallView: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "checklist")
                    .font(.title3)
                    .foregroundStyle(.indigo)
                Spacer()
                Text("Tasks")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("\(entry.data.pending)")
                        .font(.system(size: 28, weight: .bold, design: .rounded))
                        .foregroundStyle(.gray)
                    Text("pending")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text("\(entry.data.blocked)")
                        .font(.system(size: 28, weight: .bold, design: .rounded))
                        .foregroundStyle(.red)
                    Text("blocked")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private var mediumView: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Text("Tasks")
                    .font(.headline)

                HStack(spacing: 8) {
                    StatusPill(count: entry.data.pending, label: "Pending", color: .gray)
                    StatusPill(count: entry.data.inProgress, label: "Active", color: .blue)
                    StatusPill(count: entry.data.blocked, label: "Blocked", color: .red)
                    StatusPill(count: entry.data.completed, label: "Done", color: .green)
                }

                Spacer()
            }

            if !entry.data.recentTitles.isEmpty {
                Divider()

                VStack(alignment: .leading, spacing: 4) {
                    Text("Recent")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    ForEach(entry.data.recentTitles.prefix(3), id: \.self) { title in
                        Text(title)
                            .font(.caption2)
                            .lineLimit(1)
                    }
                    Spacer()
                }
            }
        }
    }
}

private struct StatusPill: View {
    let count: Int
    let label: String
    let color: Color

    var body: some View {
        VStack(spacing: 2) {
            Text("\(count)")
                .font(.system(size: 17, weight: .semibold, design: .rounded))
                .foregroundStyle(color)
            Text(label)
                .font(.system(size: 8))
                .foregroundStyle(.secondary)
        }
    }
}
