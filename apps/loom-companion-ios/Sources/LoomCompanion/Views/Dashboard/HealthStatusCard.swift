import SwiftUI
import LoomCompanionKit

/// Context card — shows server health as a proportion bar + summary.
///
/// Visually recedes to a compact single-line card when everything is healthy,
/// and expands to a standard card when degraded/down — the "alarm when wrong,
/// silent when right" principle.
struct HealthStatusCard: View {
    let health: HealthSummary

    private var totalServers: Int {
        health.healthyServers + health.degradedServers + health.downServers + health.idleServers
    }

    private var allHealthy: Bool {
        health.degradedServers == 0 && health.downServers == 0 && totalServers > 0
    }

    private var priority: LoomCardPriority {
        allHealthy ? .compact : .standard
    }

    private var accent: LoomCardAccent {
        if health.downServers > 0 { return .severity(LoomColors.statusCritical) }
        if health.degradedServers > 0 { return .severity(LoomColors.statusDegraded) }
        return .none
    }

    var body: some View {
        LoomCard(priority: priority, accent: accent) {
            if allHealthy {
                compactLayout
            } else {
                standardLayout
            }
        }
    }

    // MARK: - Compact (steady state)

    private var compactLayout: some View {
        HStack(spacing: LoomSpacing.sm) {
            Image(systemName: "checkmark.shield.fill")
                .font(.system(size: 14, weight: .semibold))
                .foregroundStyle(LoomColors.statusHealthy)
            Text("Server Health")
                .font(LoomTypography.labelLarge)
                .foregroundStyle(LoomColors.fgSecondary)
            Spacer(minLength: LoomSpacing.sm)
            healthBar
                .frame(maxWidth: 120)
            Text("\(totalServers)/\(totalServers)")
                .font(LoomTypography.monoMedium)
                .foregroundStyle(LoomColors.statusHealthy)
                .monospacedDigit()
        }
    }

    // MARK: - Standard (something wrong — expand)

    private var standardLayout: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            HStack(spacing: LoomSpacing.xs) {
                Text("Server Health")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.fgPrimary)
                Spacer()
                StatusBadge(healthStatus: health.overallStatus)
                    .animateOnStatusChange(health.overallStatus.rawValue)
            }
            HStack(spacing: LoomSpacing.md) {
                healthBar
                summaryText
            }
        }
    }

    // MARK: - Bars

    private var healthBar: some View {
        GeometryReader { geo in
            HStack(spacing: 1) {
                if health.healthyServers > 0 {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(LoomColors.statusHealthy)
                        .frame(width: segmentWidth(geo, count: health.healthyServers))
                }
                if health.degradedServers > 0 {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(LoomColors.statusDegraded)
                        .frame(width: segmentWidth(geo, count: health.degradedServers))
                }
                if health.downServers > 0 {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(LoomColors.statusCritical)
                        .frame(width: segmentWidth(geo, count: health.downServers))
                }
                if health.idleServers > 0 {
                    RoundedRectangle(cornerRadius: 3)
                        .fill(LoomColors.statusIdle.opacity(0.4))
                        .frame(width: segmentWidth(geo, count: health.idleServers))
                }
            }
        }
        .frame(height: allHealthy ? 6 : 8)
        .clipShape(RoundedRectangle(cornerRadius: 4))
    }

    private func segmentWidth(_ geo: GeometryProxy, count: Int) -> CGFloat {
        guard totalServers > 0 else { return 0 }
        let spacing = CGFloat(max(0, nonZeroSegments - 1))
        let available = geo.size.width - spacing
        return max(2, available * CGFloat(count) / CGFloat(totalServers))
    }

    private var nonZeroSegments: Int {
        [health.healthyServers, health.degradedServers, health.downServers, health.idleServers]
            .filter { $0 > 0 }.count
    }

    private var summaryText: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
            Text("\(health.healthyServers)/\(totalServers) healthy")
                .font(LoomTypography.labelSmall)
                .foregroundStyle(LoomColors.fgPrimary)

            if health.degradedServers > 0 || health.downServers > 0 {
                HStack(spacing: LoomSpacing.sm) {
                    if health.degradedServers > 0 {
                        Text("\(health.degradedServers) degraded")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.statusDegraded)
                    }
                    if health.downServers > 0 {
                        Text("\(health.downServers) down")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.statusCritical)
                    }
                }
            }
        }
    }
}
