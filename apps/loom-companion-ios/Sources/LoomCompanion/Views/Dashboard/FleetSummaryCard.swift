import SwiftUI
import LoomCompanionKit

struct FleetSummaryCard: View {
    let dashboard: DashboardData
    @State private var tileAnimationTrigger = false

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.cardSpacing) {
                HStack {
                    Text("Fleet Overview")
                        .font(LoomTypography.headlineMedium)
                    Spacer()
                    HStack(spacing: LoomSpacing.xxs) {
                        PulsingDot(
                            color: dashboard.daemonRunning
                                ? LoomColors.statusHealthy
                                : LoomColors.statusCritical,
                            isPulsing: dashboard.daemonRunning
                        )
                        Text(dashboard.daemonRunning ? "Daemon running" : "Daemon stopped")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                }

                #if canImport(Charts)
                FleetCompositionChart(
                    active: dashboard.activeAgents,
                    idle: dashboard.idleAgents,
                    offline: dashboard.offlineAgents
                )
                #endif

                LazyVGrid(columns: [
                    GridItem(.flexible()),
                    GridItem(.flexible()),
                    GridItem(.flexible()),
                ], spacing: LoomSpacing.cardSpacing) {
                    SummaryTile(
                        label: "Sessions",
                        value: dashboard.activeSessions,
                        icon: "rectangle.stack",
                        index: 0,
                        trigger: tileAnimationTrigger
                    )
                    SummaryTile(
                        label: "Active",
                        value: dashboard.activeAgents,
                        icon: "person.fill",
                        index: 1,
                        trigger: tileAnimationTrigger
                    )
                    SummaryTile(
                        label: "Idle",
                        value: dashboard.idleAgents,
                        icon: "person",
                        index: 2,
                        trigger: tileAnimationTrigger
                    )
                    SummaryTile(
                        label: "Offline",
                        value: dashboard.offlineAgents,
                        icon: "person.slash",
                        index: 3,
                        trigger: tileAnimationTrigger
                    )
                    SummaryTile(
                        label: "Servers",
                        value: dashboard.serverCount,
                        icon: "server.rack",
                        index: 4,
                        trigger: tileAnimationTrigger
                    )
                }

                if eventDensity.count > 1 {
                    VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                        Text("Event Activity")
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                        CompactSparkline(data: eventDensity, lineColor: LoomColors.accent)
                            .frame(height: 24)
                    }
                }
            }
        }
        .onAppear { tileAnimationTrigger.toggle() }
    }

    private static let isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let isoFallback: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private var eventDensity: [Double] {
        let dates = dashboard.recentTimeline.compactMap {
            Self.isoFormatter.date(from: $0.timestamp) ?? Self.isoFallback.date(from: $0.timestamp)
        }.sorted()
        guard dates.count > 1, let lo = dates.first, let hi = dates.last else { return [] }
        let span = hi.timeIntervalSince(lo)
        guard span > 0 else { return [] }
        let bucketCount = 12
        var buckets = [Double](repeating: 0, count: bucketCount)
        for d in dates {
            let idx = min(bucketCount - 1, Int(d.timeIntervalSince(lo) / span * Double(bucketCount)))
            buckets[idx] += 1
        }
        return buckets
    }
}

private struct SummaryTile: View {
    let label: String
    let value: Int
    let icon: String
    let index: Int
    let trigger: Bool

    var body: some View {
        VStack(spacing: LoomSpacing.xs) {
            Image(systemName: icon)
                .font(.title3)
                .foregroundStyle(LoomColors.textSecondary)
                .symbolEffect(.bounce, value: trigger)

            AnimatedCounter(value, font: LoomTypography.counterSmall)

            Text(label)
                .font(LoomTypography.labelSmall)
                .foregroundStyle(LoomColors.textSecondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, LoomSpacing.sm)
        .cardAppear(index: index)
    }
}
