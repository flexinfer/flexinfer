import SwiftUI
import WidgetKit
import LoomCompanionKit

struct AttentionLaneWidget: Widget {
    let kind = "AttentionLaneWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: AttentionLaneProvider()) { entry in
            AttentionLaneWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
                .widgetURL(entry.primary.flatMap { AttentionLaneDeepLink.url(for: $0) }
                           ?? URL(string: "loom://dashboard"))
        }
        .configurationDisplayName("Attention Lane")
        .description("Shows the top attention lane that needs your action.")
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

struct AttentionLaneEntry: WidgetKit.TimelineEntry {
    let date: Date
    let lanes: [AttentionLaneWidgetEntry]

    var primary: AttentionLaneWidgetEntry? { lanes.first }
}

struct AttentionLaneProvider: TimelineProvider {
    func placeholder(in context: Context) -> AttentionLaneEntry {
        AttentionLaneEntry(date: .now, lanes: SharedDataStore.placeholder.attentionLanes)
    }

    func getSnapshot(in context: Context, completion: @escaping (AttentionLaneEntry) -> Void) {
        let lanes = SharedDataStore.load()?.attentionLanes ?? SharedDataStore.placeholder.attentionLanes
        completion(AttentionLaneEntry(date: .now, lanes: lanes))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<AttentionLaneEntry>) -> Void) {
        let lanes = SharedDataStore.load()?.attentionLanes ?? []
        let entry = AttentionLaneEntry(date: .now, lanes: lanes)
        let nextUpdate = Calendar.current.date(byAdding: .minute, value: 15, to: .now) ?? .now
        completion(Timeline(entries: [entry], policy: .after(nextUpdate)))
    }
}

enum AttentionLaneDeepLink {
    static func url(for lane: AttentionLaneWidgetEntry) -> URL? {
        let route = lane.route.isEmpty ? "dashboard" : lane.route
        return URL(string: "loom://\(route)")
    }
}

struct AttentionLaneWidgetView: View {
    let entry: AttentionLaneEntry
    @Environment(\.widgetFamily) var family

    var body: some View {
        if let lane = entry.primary {
            switch family {
            case .systemSmall:
                smallView(for: lane)
            case .systemMedium:
                mediumView(for: lane, remaining: max(entry.lanes.count - 1, 0))
            default:
                smallView(for: lane)
            }
        } else {
            emptyView
        }
    }

    private func smallView(for lane: AttentionLaneWidgetEntry) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: iconName(for: lane.severity))
                    .foregroundStyle(color(for: lane.severity))
                    .font(.title3)
                Spacer()
                Text(lane.label)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 0)

            VStack(alignment: .leading, spacing: 2) {
                Text(lane.summary.isEmpty ? "Needs attention" : lane.summary)
                    .font(.subheadline)
                    .fontWeight(.semibold)
                    .lineLimit(3)
                if !lane.scope.isEmpty {
                    Text(lane.scope)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
    }

    private func mediumView(for lane: AttentionLaneWidgetEntry, remaining: Int) -> some View {
        HStack(alignment: .top, spacing: 16) {
            Image(systemName: iconName(for: lane.severity))
                .foregroundStyle(color(for: lane.severity))
                .font(.title2)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(lane.label)
                        .font(.headline)
                    Spacer()
                    Text(routeHint(for: lane.route))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Text(lane.summary.isEmpty ? "Needs attention" : lane.summary)
                    .font(.subheadline)
                    .lineLimit(2)
                if !lane.scope.isEmpty {
                    Text(lane.scope)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
                if remaining > 0 {
                    Text("+\(remaining) more")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private var emptyView: some View {
        VStack(spacing: 6) {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(.green)
                .font(.title2)
            Text("All clear")
                .font(.headline)
            Text("No attention lanes")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func iconName(for severity: String) -> String {
        switch severity {
        case "critical": return "exclamationmark.octagon.fill"
        case "warning": return "exclamationmark.triangle.fill"
        default: return "bell.fill"
        }
    }

    private func color(for severity: String) -> Color {
        switch severity {
        case "critical": return .red
        case "warning": return .orange
        default: return .blue
        }
    }

    private func routeHint(for route: String) -> String {
        switch route {
        case "people": return "People"
        case "work": return "Work"
        case "dispatch": return "Dispatch"
        default: return route.capitalized
        }
    }
}
