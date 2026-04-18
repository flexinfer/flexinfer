import SwiftUI
import LoomCompanionKit

/// Context card — fleet composition at a glance.
///
/// Uses compact variant when the fleet is steady (daemon running, no offline
/// agents, nothing unusual). Expands to standard when composition indicates
/// something worth drill-down (offline agents present, daemon stopped,
/// noteworthy event density).
struct FleetSummaryCard: View {
    let dashboard: DashboardData
    @State private var tileAnimationTrigger = false

    private var hasAnomaly: Bool {
        !dashboard.daemonRunning || dashboard.offlineAgents > 0
    }

    private var priority: LoomCardPriority {
        hasAnomaly ? .standard : .compact
    }

    private var accent: LoomCardAccent {
        if !dashboard.daemonRunning { return .severity(LoomColors.statusCritical) }
        if dashboard.offlineAgents > 0 { return .severity(LoomColors.statusDegraded) }
        return .none
    }

    var body: some View {
        LoomCard(priority: priority, accent: accent) {
            if hasAnomaly {
                standardLayout
            } else {
                compactLayout
            }
        }
        .onAppear { tileAnimationTrigger.toggle() }
    }

    // MARK: - Compact (steady)

    private var compactLayout: some View {
        HStack(spacing: LoomSpacing.md) {
            daemonIndicator
            VStack(alignment: .leading, spacing: 0) {
                Text("Fleet")
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(LoomColors.fgSecondary)
                HStack(spacing: LoomSpacing.xs) {
                    inlineCount(count: dashboard.activeAgents, label: "active", color: LoomColors.statusHealthy)
                    Text("·").foregroundStyle(LoomColors.fgMuted)
                    inlineCount(count: dashboard.idleAgents, label: "idle", color: LoomColors.fgSecondary)
                    Text("·").foregroundStyle(LoomColors.fgMuted)
                    inlineCount(count: dashboard.activeSessions, label: "sessions", color: LoomColors.statusActive)
                }
            }
            Spacer()
            if eventDensity.count > 1 {
                CompactSparkline(data: eventDensity, lineColor: LoomColors.accent)
                    .frame(width: 80, height: 22)
            }
        }
    }

    private var daemonIndicator: some View {
        VStack(spacing: 2) {
            PulsingDot(
                color: dashboard.daemonRunning ? LoomColors.statusHealthy : LoomColors.statusCritical,
                size: 9,
                isPulsing: dashboard.daemonRunning
            )
            Text("DAEMON")
                .font(LoomTypography.sectionTitle)
                .tracking(1.1)
                .foregroundStyle(LoomColors.fgMuted)
        }
        .frame(width: 44)
    }

    private func inlineCount(count: Int, label: String, color: Color) -> some View {
        HStack(spacing: 2) {
            Text("\(count)")
                .font(LoomTypography.monoMedium)
                .foregroundStyle(count > 0 ? color : LoomColors.fgMuted)
                .monospacedDigit()
            Text(label)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.fgMuted)
        }
    }

    // MARK: - Standard (anomaly)

    private var standardLayout: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            HStack {
                Text("Fleet Overview")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.fgPrimary)
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
                        .foregroundStyle(
                            dashboard.daemonRunning
                                ? LoomColors.textSecondary
                                : LoomColors.statusCritical
                        )
                }
            }

            if let heartbeat = dashboard.lastHeartbeat {
                HStack(spacing: LoomSpacing.xxs) {
                    PulsingDot(color: LoomColors.statusHealthy, isPulsing: true)
                    Text("Last heartbeat \(relativeHeartbeatTime(heartbeat.timestamp))")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                    if !heartbeat.agentId.isEmpty {
                        Text("· \(heartbeat.agentId)")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                    Text("· \(heartbeat.count1h)/h")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }

            #if canImport(Charts)
            FleetCompositionChart(
                active: dashboard.activeAgents,
                idle: dashboard.idleAgents,
                offline: dashboard.offlineAgents
            )
            #endif

            HStack(spacing: 0) {
                compactMetric(count: dashboard.activeSessions, label: "Sessions", index: 0)
                metricDivider
                compactMetric(count: dashboard.activeAgents, label: "Active", index: 1)
                metricDivider
                compactMetric(count: dashboard.idleAgents, label: "Idle", index: 2)
                if dashboard.offlineAgents > 0 {
                    metricDivider
                    compactMetric(count: dashboard.offlineAgents, label: "Offline", index: 3)
                }
                metricDivider
                compactMetric(count: dashboard.serverCount, label: "Srv", index: 4)
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
        Divider().frame(height: 28)
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

    private func relativeHeartbeatTime(_ iso: String) -> String {
        guard let date = Self.isoFormatter.date(from: iso) ?? Self.isoFallback.date(from: iso) else {
            return "just now"
        }
        let diff = Int(Date().timeIntervalSince(date))
        if diff < 5 { return "just now" }
        if diff < 60 { return "\(diff)s ago" }
        if diff < 3600 { return "\(diff / 60)m ago" }
        return "\(diff / 3600)h ago"
    }

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
