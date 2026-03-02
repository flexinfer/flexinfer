import SwiftUI
import WidgetKit
import LoomCompanionKit

struct FleetHealthWidget: Widget {
    let kind = "FleetHealthWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: FleetHealthProvider()) { entry in
            FleetHealthWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Fleet Health")
        .description("Monitor your Loom fleet health at a glance.")
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

struct FleetHealthEntry: TimelineEntry {
    let date: Date
    let data: FleetWidgetData
}

struct FleetHealthProvider: TimelineProvider {
    func placeholder(in context: Context) -> FleetHealthEntry {
        FleetHealthEntry(date: .now, data: SharedDataStore.placeholder.fleet)
    }

    func getSnapshot(in context: Context, completion: @escaping (FleetHealthEntry) -> Void) {
        let data = SharedDataStore.load()?.fleet ?? SharedDataStore.placeholder.fleet
        completion(FleetHealthEntry(date: .now, data: data))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<FleetHealthEntry>) -> Void) {
        let data = SharedDataStore.load()?.fleet ?? SharedDataStore.placeholder.fleet
        let entry = FleetHealthEntry(date: .now, data: data)
        let nextUpdate = Calendar.current.date(byAdding: .minute, value: 15, to: .now) ?? .now
        completion(Timeline(entries: [entry], policy: .after(nextUpdate)))
    }
}

struct FleetHealthWidgetView: View {
    let entry: FleetHealthEntry
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
                Image(systemName: entry.data.daemonRunning ? "checkmark.circle.fill" : "xmark.circle.fill")
                    .foregroundStyle(entry.data.daemonRunning ? .green : .red)
                    .font(.title2)
                Spacer()
                Text("Loom")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            VStack(alignment: .leading, spacing: 2) {
                Text("\(entry.data.healthyServers)")
                    .font(.system(size: 34, weight: .bold, design: .rounded))
                Text("healthy servers")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if entry.data.degradedServers > 0 || entry.data.downServers > 0 {
                HStack(spacing: 6) {
                    if entry.data.degradedServers > 0 {
                        Label("\(entry.data.degradedServers)", systemImage: "exclamationmark.triangle")
                            .font(.caption2)
                            .foregroundStyle(.orange)
                    }
                    if entry.data.downServers > 0 {
                        Label("\(entry.data.downServers)", systemImage: "xmark.circle")
                            .font(.caption2)
                            .foregroundStyle(.red)
                    }
                }
            }
        }
    }

    private var mediumView: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Image(systemName: entry.data.daemonRunning ? "checkmark.circle.fill" : "xmark.circle.fill")
                        .foregroundStyle(entry.data.daemonRunning ? .green : .red)
                    Text("Fleet")
                        .font(.headline)
                }

                VStack(alignment: .leading, spacing: 2) {
                    Text("\(entry.data.healthyServers)/\(entry.data.serverCount)")
                        .font(.system(size: 28, weight: .bold, design: .rounded))
                    Text("servers healthy")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer()
            }

            Divider()

            VStack(alignment: .leading, spacing: 6) {
                MetricRow(icon: "rectangle.stack", label: "Sessions", value: entry.data.sessionCount, color: .blue)
                MetricRow(icon: "person.fill", label: "Active", value: entry.data.activeAgents, color: .green)
                MetricRow(icon: "person", label: "Idle", value: entry.data.idleAgents, color: .gray)
                MetricRow(icon: "person.slash", label: "Offline", value: entry.data.offlineAgents, color: .red)
            }
        }
    }
}

private struct MetricRow: View {
    let icon: String
    let label: String
    let value: Int
    let color: Color

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: icon)
                .font(.caption)
                .foregroundStyle(color)
                .frame(width: 16)
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Text("\(value)")
                .font(.caption)
                .fontWeight(.semibold)
        }
    }
}
