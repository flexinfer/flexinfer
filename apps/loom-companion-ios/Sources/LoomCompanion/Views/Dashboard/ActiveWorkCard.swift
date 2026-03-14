import SwiftUI
import LoomCompanionKit

struct ActiveWorkCard: View {
    let counts: MobileTaskCounts

    private var total: Int {
        counts.pending + counts.inProgress + counts.blocked
    }

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Active Work")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                HStack(spacing: LoomSpacing.lg) {
                    metricPill(count: counts.pending, label: "Pending", color: .orange)
                    metricPill(count: counts.inProgress, label: "Active", color: LoomColors.statusHealthy)
                    metricPill(count: counts.blocked, label: "Blocked", color: LoomColors.statusBlocked)

                    Spacer()

                    if total > 0 {
                        Text("\(total)")
                            .font(LoomTypography.counterLarge)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                }

                if total > 0 {
                    ProportionBar(segments: [
                        (Double(counts.pending), Color.orange),
                        (Double(counts.inProgress), LoomColors.statusHealthy),
                        (Double(counts.blocked), LoomColors.statusBlocked),
                    ])
                    .frame(height: 4)
                }

                if counts.completed > 0 {
                    Text("\(counts.completed) completed")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    private func metricPill(count: Int, label: String, color: Color) -> some View {
        VStack(spacing: 2) {
            Text("\(count)")
                .font(LoomTypography.counterMedium)
                .foregroundStyle(count > 0 ? color : LoomColors.textTertiary)
            Text(label)
                .font(LoomTypography.monoCaption)
                .foregroundStyle(LoomColors.textSecondary)
        }
    }
}
