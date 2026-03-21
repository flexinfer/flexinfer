import SwiftUI
import LoomCompanionKit

struct HealthStatusCard: View {
    let health: HealthSummary

    private var totalServers: Int {
        health.healthyServers + health.degradedServers + health.downServers + health.idleServers
    }

    private var allHealthy: Bool {
        health.degradedServers == 0 && health.downServers == 0 && totalServers > 0
    }

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                HStack {
                    Text("Server Health")
                        .font(LoomTypography.headlineMedium)
                    Spacer()
                    StatusBadge(healthStatus: health.overallStatus)
                        .animateOnStatusChange(health.overallStatus.rawValue)
                }

                if allHealthy {
                    // Collapsed single-line when all healthy
                    HStack(spacing: LoomSpacing.sm) {
                        healthBar
                        Text("\(totalServers)/\(totalServers) healthy")
                            .font(LoomTypography.labelSmall)
                            .foregroundStyle(LoomColors.statusHealthy)
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 12))
                            .foregroundStyle(LoomColors.statusHealthy)
                    }
                } else {
                    // Bar + summary text
                    HStack(spacing: LoomSpacing.md) {
                        healthBar
                        summaryText
                    }
                }
            }
        }
    }

    // MARK: - Health Proportion Bar

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
        .frame(height: 8)
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

    // MARK: - Summary Text

    private var summaryText: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
            Text("\(health.healthyServers)/\(totalServers) healthy")
                .font(LoomTypography.labelSmall)
                .foregroundStyle(LoomColors.textPrimary)

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
