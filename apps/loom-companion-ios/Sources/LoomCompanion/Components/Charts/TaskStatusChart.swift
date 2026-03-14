import SwiftUI

#if canImport(Charts)
import Charts
import LoomCompanionKit

struct TaskStatusChart: View {
    let pending: Int
    let inProgress: Int
    let blocked: Int
    let completed: Int

    private var segments: [(label: String, value: Int, color: Color)] {
        [
            ("Pending", pending, LoomColors.statusIdle),
            ("Active", inProgress, LoomColors.statusActive),
            ("Blocked", blocked, LoomColors.statusBlocked),
            ("Done", completed, LoomColors.statusHealthy),
        ]
    }

    private var total: Int {
        pending + inProgress + blocked + completed
    }

    var body: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            Chart(segments, id: \.label) { segment in
                BarMark(
                    x: .value("Count", segment.value)
                )
                .foregroundStyle(segment.color)
                .cornerRadius(4)
            }
            .chartXAxis(.hidden)
            .chartYAxis(.hidden)
            .frame(height: 24)
            .clipShape(RoundedRectangle(cornerRadius: 6))

            HStack(spacing: LoomSpacing.md) {
                ForEach(segments, id: \.label) { segment in
                    HStack(spacing: LoomSpacing.xxs) {
                        Circle()
                            .fill(segment.color)
                            .frame(width: 6, height: 6)
                        Text("\(segment.value)")
                            .font(LoomTypography.labelSmall)
                            .fontWeight(.semibold)
                        Text(segment.label)
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                }
            }
        }
    }
}

#Preview("TaskStatusChart") {
    TaskStatusChart(pending: 5, inProgress: 3, blocked: 2, completed: 12)
        .padding()
}
#endif
