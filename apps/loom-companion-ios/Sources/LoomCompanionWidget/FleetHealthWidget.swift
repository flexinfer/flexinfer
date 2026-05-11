import SwiftUI
import WidgetKit
import LoomCompanionKit

struct FleetHealthWidget: Widget {
    let kind = "FleetHealthWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: FleetHealthProvider()) { entry in
            FleetHealthWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
                .widgetURL(URL(string: "loom://dashboard"))
        }
        .configurationDisplayName("Fleet Health")
        .description("Monitor your Loom fleet health at a glance.")
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

struct FleetHealthEntry: WidgetKit.TimelineEntry {
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
            HStack(spacing: 6) {
                Image(systemName: statusIcon)
                    .foregroundStyle(statusColor)
                    .font(.title3.weight(.semibold))
                Text(statusTitle)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.primary)
                    .lineLimit(1)
                Spacer()
            }

            Spacer()

            VStack(alignment: .leading, spacing: 3) {
                Text("\(entry.data.healthyServers)/\(entry.data.serverCount)")
                    .font(.system(size: 32, weight: .bold, design: .rounded))
                    .monospacedDigit()
                    .minimumScaleFactor(0.8)
                Text(statusSubtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            healthBar
                .frame(height: 5)

            HStack(spacing: 6) {
                Label("\(entry.data.activeAgents) active", systemImage: "person.fill")
                    .foregroundStyle(.green)
                Label("\(entry.data.sessionCount) sessions", systemImage: "rectangle.stack")
                    .foregroundStyle(.blue)
            }
            .font(.caption2.weight(.medium))
            .lineLimit(1)
        }
    }

    private var healthBar: some View {
        GeometryReader { geo in
            let total = max(entry.data.serverCount, 1)
            HStack(spacing: 1) {
                if entry.data.healthyServers > 0 {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(.green)
                        .frame(width: geo.size.width * CGFloat(entry.data.healthyServers) / CGFloat(total))
                }
                if entry.data.degradedServers > 0 {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(.orange)
                        .frame(width: geo.size.width * CGFloat(entry.data.degradedServers) / CGFloat(total))
                }
                if entry.data.downServers > 0 {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(.red)
                        .frame(width: geo.size.width * CGFloat(entry.data.downServers) / CGFloat(total))
                }
            }
        }
    }

    private var mediumView: some View {
        HStack(spacing: 14) {
            VStack(alignment: .leading, spacing: 9) {
                HStack(spacing: 8) {
                    Image(systemName: statusIcon)
                        .font(.title3.weight(.semibold))
                        .foregroundStyle(statusColor)
                    Text("Fleet")
                        .font(.headline)
                        .lineLimit(1)
                    Spacer()
                }

                VStack(alignment: .leading, spacing: 3) {
                    Text("\(entry.data.healthyServers)/\(entry.data.serverCount)")
                        .font(.system(size: 34, weight: .bold, design: .rounded))
                        .monospacedDigit()
                        .minimumScaleFactor(0.75)
                        .contentTransition(.numericText())
                    Text(statusSubtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                healthBar
                    .frame(height: 6)

                Spacer()
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Divider()
                .foregroundStyle(.secondary.opacity(0.3))

            VStack(spacing: 8) {
                HStack(spacing: 8) {
                    MetricTile(icon: "rectangle.stack.fill", label: "Sessions", value: entry.data.sessionCount, color: .blue)
                    MetricTile(icon: "person.fill", label: "Active", value: entry.data.activeAgents, color: .green)
                }
                HStack(spacing: 8) {
                    MetricTile(icon: "person", label: "Idle", value: entry.data.idleAgents, color: .gray)
                    MetricTile(icon: "person.slash", label: "Offline", value: entry.data.offlineAgents, color: .red)
                }
                Spacer(minLength: 0)
            }
            .frame(maxWidth: .infinity, alignment: .top)
        }
    }

    private var statusIcon: String {
        if !entry.data.daemonRunning { return "xmark.circle.fill" }
        if entry.data.downServers > 0 { return "exclamationmark.triangle.fill" }
        if entry.data.degradedServers > 0 { return "exclamationmark.circle.fill" }
        return "checkmark.circle.fill"
    }

    private var statusTitle: String {
        if !entry.data.daemonRunning { return "Offline" }
        if entry.data.downServers > 0 { return "Down" }
        if entry.data.degradedServers > 0 { return "Degraded" }
        return "Healthy"
    }

    private var statusSubtitle: String {
        if !entry.data.daemonRunning { return "daemon offline" }
        if entry.data.downServers > 0 {
            var parts = ["\(entry.data.downServers) down"]
            if entry.data.degradedServers > 0 {
                parts.append("\(entry.data.degradedServers) degraded")
            }
            return parts.joined(separator: " · ")
        }
        if entry.data.degradedServers > 0 {
            return "\(entry.data.degradedServers) degraded"
        }
        return "servers healthy"
    }

    private var statusColor: Color {
        if !entry.data.daemonRunning || entry.data.downServers > 0 { return .red }
        if entry.data.degradedServers > 0 { return .orange }
        return .green
    }
}

private struct MetricTile: View {
    let icon: String
    let label: String
    let value: Int
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Image(systemName: icon)
                .font(.caption.weight(.semibold))
                .foregroundStyle(color)
            Text(label)
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(.secondary)
            Text("\(value)")
                .font(.system(size: 16, weight: .semibold, design: .rounded))
                .monospacedDigit()
                .foregroundStyle(.primary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
