import SwiftUI
import LoomCompanionKit

struct ActiveWorkCard: View {
    let counts: MobileTaskCounts

    private var total: Int {
        counts.pending + counts.inProgress + counts.blocked
    }

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                // Single row: title + colored count badges
                HStack(spacing: LoomSpacing.md) {
                    Text("Active Work")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    Spacer()

                    inlineCount(count: counts.pending, label: "pending", color: .orange)
                    inlineCount(count: counts.inProgress, label: "active", color: LoomColors.statusHealthy)
                    inlineCount(count: counts.blocked, label: "blocked", color: LoomColors.statusBlocked)
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

    private func inlineCount(count: Int, label: String, color: Color) -> some View {
        HStack(spacing: LoomSpacing.xxs) {
            Text("\(count)")
                .font(LoomTypography.counterSmall)
                .foregroundStyle(count > 0 ? color : LoomColors.textTertiary)
            Text(label)
                .font(LoomTypography.monoCaption)
                .foregroundStyle(LoomColors.textSecondary)
        }
    }
}
