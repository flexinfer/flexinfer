import SwiftUI
import LoomCompanionKit

struct HealthStatusCard: View {
    let health: HealthSummary

    private var totalServers: Int {
        health.healthyServers + health.degradedServers + health.downServers + health.idleServers
    }

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.cardSpacing) {
                HStack {
                    Text("Server Health")
                        .font(LoomTypography.headlineMedium)
                    Spacer()
                    StatusBadge(healthStatus: health.overallStatus)
                        .animateOnStatusChange(health.overallStatus.rawValue)
                }

                #if canImport(Charts)
                HealthGaugeChart(health: health)
                    .frame(height: 140)
                    .padding(.vertical, LoomSpacing.xxs)
                #else
                HStack(spacing: 0) {
                    Spacer()
                    Gauge(
                        value: Double(health.healthyServers),
                        in: 0...Double(max(totalServers, 1))
                    ) {
                        Text("Health")
                    } currentValueLabel: {
                        Text("\(health.healthyServers)/\(totalServers)")
                            .font(LoomTypography.labelSmall)
                    }
                    .gaugeStyle(.accessoryCircular)
                    .tint(Gradient(colors: [
                        LoomColors.statusCritical,
                        LoomColors.statusDegraded,
                        LoomColors.statusHealthy,
                    ]))
                    .scaleEffect(1.4)
                    .frame(width: 56, height: 56)
                    Spacer()
                }
                .padding(.vertical, LoomSpacing.xs)
                #endif

                HStack(spacing: LoomSpacing.xl) {
                    HealthMetric(
                        label: "Healthy",
                        count: health.healthyServers,
                        color: LoomColors.statusHealthy,
                        icon: "checkmark.circle.fill"
                    )
                    HealthMetric(
                        label: "Degraded",
                        count: health.degradedServers,
                        color: LoomColors.statusDegraded,
                        icon: "exclamationmark.triangle.fill"
                    )
                    HealthMetric(
                        label: "Down",
                        count: health.downServers,
                        color: LoomColors.statusCritical,
                        icon: "xmark.circle.fill"
                    )
                    HealthMetric(
                        label: "Idle",
                        count: health.idleServers,
                        color: LoomColors.statusIdle,
                        icon: "moon.fill"
                    )
                }
            }
        }
    }
}

private struct HealthMetric: View {
    let label: String
    let count: Int
    let color: Color
    let icon: String

    var body: some View {
        VStack(spacing: LoomSpacing.xxs) {
            Image(systemName: icon)
                .font(.caption)
                .foregroundStyle(color.opacity(0.7))
                .symbolEffect(.bounce, value: count)
            AnimatedCounter(count, font: LoomTypography.counterMedium, color: color)
            Text(label)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        }
    }
}
