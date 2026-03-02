import SwiftUI

#if canImport(Charts)
import Charts
import LoomCompanionKit

struct HealthGaugeChart: View {
    let health: HealthSummary

    private var segments: [(label: String, value: Int, color: Color)] {
        [
            ("Healthy", health.healthyServers, LoomColors.statusHealthy),
            ("Degraded", health.degradedServers, LoomColors.statusDegraded),
            ("Down", health.downServers, LoomColors.statusCritical),
            ("Idle", health.idleServers, LoomColors.statusIdle),
        ].filter { $0.value > 0 }
    }

    var body: some View {
        Chart(segments, id: \.label) { segment in
            SectorMark(
                angle: .value(segment.label, segment.value),
                innerRadius: .ratio(0.65),
                angularInset: 1.5
            )
            .foregroundStyle(segment.color)
            .cornerRadius(3)
        }
        .chartBackground { _ in
            VStack(spacing: 2) {
                Text("\(health.healthyServers)")
                    .font(LoomTypography.counterMedium)
                    .foregroundStyle(LoomColors.statusHealthy)
                Text("healthy")
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.textSecondary)
            }
        }
        .frame(height: 120)
    }
}

#Preview("HealthGaugeChart") {
    HealthGaugeChart(health: HealthSummary(
        totalServers: 14,
        healthyServers: 8,
        degradedServers: 2,
        downServers: 1,
        idleServers: 3
    ))
    .padding()
}
#endif
