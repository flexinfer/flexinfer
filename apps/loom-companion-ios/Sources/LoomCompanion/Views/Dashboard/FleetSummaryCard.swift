import SwiftUI
import LoomCompanionKit

struct FleetSummaryCard: View {
    let dashboard: DashboardData
    @State private var tileAnimationTrigger = false

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
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

                // Compact inline metrics strip
                HStack(spacing: 0) {
                    compactMetric(
                        count: dashboard.activeSessions,
                        label: "Sessions",
                        index: 0
                    )
                    metricDivider
                    compactMetric(
                        count: dashboard.activeAgents,
                        label: "Active",
                        index: 1
                    )
                    metricDivider
                    compactMetric(
                        count: dashboard.idleAgents,
                        label: "Idle",
                        index: 2
                    )
                    if dashboard.offlineAgents > 0 {
                        metricDivider
                        compactMetric(
                            count: dashboard.offlineAgents,
                            label: "Offline",
                            index: 3
                        )
                    }
                    metricDivider
                    compactMetric(
                        count: dashboard.serverCount,
                        label: "Srv",
                        index: 4
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

    // MARK: - Compact Metric Cell

    private func compactMetric(count: Int, label: String, index: Int) -> some View {
        VStack(spacing: LoomSpacing.xxs) {
            AnimatedCounter(count, font: LoomTypography.counterSmall)
            Text(label)
                .font(LoomTypography.labelSmall)
                .foregroundStyle(LoomColors.textSecondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, LoomSpacing.xs)
        .cardAppear(index: index)
    }

    private var metricDivider: some View {
        Divider()
            .frame(height: 28)
    }

    // MARK: - Event Density

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
