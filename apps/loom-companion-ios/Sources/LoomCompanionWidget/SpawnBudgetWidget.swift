import SwiftUI
import WidgetKit
import LoomCompanionKit

struct SpawnBudgetWidget: Widget {
    let kind = SpawnBudgetWidgetKind

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: SpawnBudgetProvider()) { entry in
            SpawnBudgetWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
                .widgetURL(URL(string: "loom://spawn"))
        }
        .configurationDisplayName("Spawn Budget")
        .description("Live cost and turn pressure for active headless agent spawns.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}

struct SpawnBudgetEntry: WidgetKit.TimelineEntry {
    let date: Date
    let data: SpawnBudgetWidgetData
}

struct SpawnBudgetProvider: TimelineProvider {
    func placeholder(in context: Context) -> SpawnBudgetEntry {
        SpawnBudgetEntry(date: .now, data: SharedDataStore.placeholderSpawnBudget)
    }

    func getSnapshot(in context: Context, completion: @escaping (SpawnBudgetEntry) -> Void) {
        let data = SharedDataStore.loadSpawnBudget() ?? SharedDataStore.placeholderSpawnBudget
        completion(SpawnBudgetEntry(date: .now, data: data))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<SpawnBudgetEntry>) -> Void) {
        let data = SharedDataStore.loadSpawnBudget() ?? SpawnBudgetWidgetData(entries: [])
        let entry = SpawnBudgetEntry(date: .now, data: data)
        // 5 min refresh keeps the widget responsive without thrashing the
        // network when no SSE delta arrives between view-model publishes.
        let nextUpdate = Calendar.current.date(byAdding: .minute, value: 5, to: .now) ?? .now
        completion(Timeline(entries: [entry], policy: .after(nextUpdate)))
    }
}

struct SpawnBudgetWidgetView: View {
    let entry: SpawnBudgetEntry
    @Environment(\.widgetFamily) var family

    private var rowLimit: Int {
        switch family {
        case .systemSmall: return 1
        case .systemMedium: return 2
        case .systemLarge: return 5
        default: return 2
        }
    }

    private var topEntries: [SpawnBudgetWidgetEntry] {
        Array(entry.data.entries.prefix(rowLimit))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            header
            if entry.data.entries.isEmpty {
                emptyState
            } else if family == .systemSmall {
                if let first = topEntries.first {
                    smallRow(first)
                }
            } else {
                ForEach(topEntries) { spawnRow($0) }
                if entry.data.entries.count > rowLimit {
                    Text("+\(entry.data.entries.count - rowLimit) more")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .padding(.top, 2)
                }
                Spacer(minLength: 0)
            }
        }
    }

    private var header: some View {
        HStack {
            Image(systemName: "bolt.horizontal.circle.fill")
                .foregroundStyle(.orange)
            Text("Spawn Budget")
                .font(.headline)
            Spacer()
            Text("\(entry.data.entries.count)")
                .font(.system(size: 22, weight: .bold, design: .rounded))
                .foregroundStyle(.orange)
                .contentTransition(.numericText())
        }
    }

    private var emptyState: some View {
        VStack {
            Spacer()
            HStack {
                Spacer()
                VStack(spacing: 4) {
                    Image(systemName: "moon.zzz")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Text("No active spawns")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }
            Spacer()
        }
    }

    private func smallRow(_ row: SpawnBudgetWidgetEntry) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(row.namespace)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                if row.costEstimated {
                    Text("~")
                        .font(.title3.weight(.semibold))
                        .foregroundStyle(.secondary)
                }
                Text(formatCost(row.totalCostUSD))
                    .font(.title2.weight(.bold))
                    .foregroundStyle(costColor(row))
                    .contentTransition(.numericText())
            }
            HStack(spacing: 4) {
                Image(systemName: "arrow.triangle.2.circlepath")
                    .font(.caption2)
                Text(turnLabel(row))
                    .font(.caption2)
            }
            .foregroundStyle(.secondary)
            if let frac = row.costFraction {
                budgetBar(fraction: frac, tint: costColor(row))
            }
        }
    }

    private func spawnRow(_ row: SpawnBudgetWidgetEntry) -> some View {
        let agentColor = agentTypeColor(row.agentType)
        return VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Image(systemName: agentTypeIcon(row.agentType))
                    .font(.caption)
                    .foregroundStyle(agentColor)
                    .frame(width: 16)
                Text(row.namespace)
                    .font(.caption)
                    .fontWeight(.medium)
                    .lineLimit(1)
                Spacer()
                Text(turnLabel(row))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                HStack(spacing: 2) {
                    if row.costEstimated {
                        Text("~")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    Text(formatCost(row.totalCostUSD))
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(costColor(row))
                        .contentTransition(.numericText())
                }
            }
            if let frac = row.costFraction {
                budgetBar(fraction: frac, tint: costColor(row))
            }
        }
        .padding(.vertical, 3)
        .padding(.horizontal, 6)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(agentColor.opacity(0.06))
        )
    }

    private func budgetBar(fraction: Double, tint: Color) -> some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(.gray.opacity(0.18))
                Capsule()
                    .fill(tint)
                    .frame(width: max(geo.size.width * CGFloat(fraction), 2))
            }
        }
        .frame(height: 4)
    }

    // MARK: - Helpers

    private func formatCost(_ usd: Double) -> String {
        if usd < 0.01 {
            return String(format: "$%.3f", usd)
        }
        return String(format: "$%.2f", usd)
    }

    private func turnLabel(_ row: SpawnBudgetWidgetEntry) -> String {
        if let cap = row.maxTurns {
            return "\(row.turnCount)/\(cap)"
        }
        return "\(row.turnCount)"
    }

    private func costColor(_ row: SpawnBudgetWidgetEntry) -> Color {
        guard let frac = row.costFraction else { return .orange }
        if frac >= 0.8 { return .red }
        if frac >= 0.5 { return .orange }
        return .green
    }

    private func agentTypeColor(_ type: String) -> Color {
        switch type.lowercased() {
        case "claude-code", "claude": return Color(red: 0.85, green: 0.55, blue: 0.25)
        case "gemini": return Color(red: 0.3, green: 0.65, blue: 0.95)
        case "codex": return Color(red: 0.4, green: 0.8, blue: 0.4)
        case "kilocode": return Color(red: 0.7, green: 0.4, blue: 0.9)
        default: return .indigo
        }
    }

    private func agentTypeIcon(_ type: String) -> String {
        switch type.lowercased() {
        case "claude-code", "claude": return "terminal.fill"
        case "gemini": return "wand.and.sparkles"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        case "kilocode": return "ruler.fill"
        default: return "cpu.fill"
        }
    }
}
