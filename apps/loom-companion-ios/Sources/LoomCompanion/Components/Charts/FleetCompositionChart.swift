import SwiftUI

#if canImport(Charts)
import Charts
import LoomCompanionKit

struct FleetCompositionChart: View {
    let active: Int
    let idle: Int
    let offline: Int

    private var segments: [(label: String, value: Int, color: Color)] {
        [
            ("Active", active, LoomColors.statusHealthy),
            ("Idle", idle, LoomColors.statusIdle),
            ("Offline", offline, LoomColors.statusCritical),
        ].filter { $0.value > 0 }
    }

    private var total: Int { active + idle + offline }

    var body: some View {
        Chart(segments, id: \.label) { segment in
            SectorMark(
                angle: .value(segment.label, segment.value),
                innerRadius: .ratio(0.7),
                angularInset: 1
            )
            .foregroundStyle(segment.color)
            .cornerRadius(2)
        }
        .chartBackground { _ in
            Text("\(total)")
                .font(LoomTypography.counterSmall)
                .foregroundStyle(LoomColors.textPrimary)
        }
        .frame(width: 64, height: 64)
    }
}

#Preview("FleetCompositionChart") {
    FleetCompositionChart(active: 5, idle: 2, offline: 1)
        .padding()
}
#endif
