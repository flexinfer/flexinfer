import SwiftUI
import LoomCompanionKit

/// Scales its own prominence with its content:
/// - Any blocked tasks → standard card with pulsing warning emphasis
/// - Only pending/active → compact single-line card
/// - All zero → not rendered (DashboardView checks and omits)
struct ActiveWorkCard: View {
    let counts: MobileTaskCounts
    var onOpen: (() -> Void)?

    private var total: Int {
        counts.pending + counts.inProgress + counts.blocked
    }

    private var hasBlocked: Bool { counts.blocked > 0 }

    private var priority: LoomCardPriority {
        hasBlocked ? .standard : .compact
    }

    private var accent: LoomCardAccent {
        hasBlocked
            ? .severity(LoomColors.statusBlocked, pulse: true)
            : .none
    }

    var body: some View {
        Group {
            if let onOpen {
                Button {
                    HapticManager.selection()
                    onOpen()
                } label: {
                    cardContent
                }
                .buttonStyle(.plain)
            } else {
                cardContent
            }
        }
    }

    @ViewBuilder
    private var cardContent: some View {
        LoomCard(priority: priority, accent: accent) {
            if hasBlocked {
                standardLayout
            } else {
                compactLayout
            }
        }
        // Animate proportion-bar segments + count transitions when backend
        // pushes new counts (compact → standard promotion also animates).
        .animation(.spring(duration: 0.5, bounce: 0.18), value: counts.pending)
        .animation(.spring(duration: 0.5, bounce: 0.18), value: counts.inProgress)
        .animation(.spring(duration: 0.5, bounce: 0.18), value: counts.blocked)
        .loomValueChangeFlash(counts.blocked, color: LoomColors.statusBlocked)
    }

    // MARK: - Standard layout (blocked present — high emphasis)

    @ViewBuilder
    private var standardLayout: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            HStack(spacing: LoomSpacing.xs) {
                Text("Active Work")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.fgPrimary)
                Spacer()
                blockedCallout
            }

            proportionBar(height: 8)

            HStack(spacing: LoomSpacing.lg) {
                metricCell(count: counts.pending, label: "pending", color: LoomColors.statusDegraded)
                metricCell(count: counts.inProgress, label: "active", color: LoomColors.statusHealthy)
                metricCell(count: counts.blocked, label: "blocked", color: LoomColors.statusBlocked)
                Spacer(minLength: 0)
            }

            if counts.completed > 0 {
                Text("\(counts.completed) completed today")
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.fgMuted)
            }
        }
    }

    private var blockedCallout: some View {
        HStack(spacing: 4) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(LoomColors.statusBlocked)
                .symbolEffect(.pulse, isActive: true)
            Text("\(counts.blocked) blocked")
                .font(LoomTypography.labelLarge)
                .foregroundStyle(LoomColors.statusBlocked)
        }
    }

    // MARK: - Compact layout (no blockers — steady)

    @ViewBuilder
    private var compactLayout: some View {
        HStack(spacing: LoomSpacing.md) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Active Work")
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(LoomColors.fgSecondary)
                HStack(spacing: LoomSpacing.xs) {
                    Text("\(total)")
                        .font(LoomTypography.counterSmall)
                        .foregroundStyle(LoomColors.fgPrimary)
                    Text("in flight")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.fgMuted)
                }
            }

            Spacer()

            proportionBar(height: 5)
                .frame(maxWidth: 140)

            inlineMini(count: counts.pending, color: LoomColors.statusDegraded, label: "P")
            inlineMini(count: counts.inProgress, color: LoomColors.statusHealthy, label: "A")
        }
    }

    private func inlineMini(count: Int, color: Color, label: String) -> some View {
        HStack(spacing: 2) {
            Text("\(count)")
                .font(LoomTypography.monoMedium)
                .foregroundStyle(count > 0 ? color : LoomColors.fgMuted)
            Text(label)
                .font(LoomTypography.monoCaption)
                .foregroundStyle(LoomColors.fgMuted)
        }
    }

    // MARK: - Shared pieces

    private func proportionBar(height: CGFloat) -> some View {
        ProportionBar(segments: [
            (Double(counts.pending), LoomColors.statusDegraded),
            (Double(counts.inProgress), LoomColors.statusHealthy),
            (Double(counts.blocked), LoomColors.statusBlocked),
        ])
        .frame(height: height)
    }

    private func metricCell(count: Int, label: String, color: Color) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("\(count)")
                .font(LoomTypography.counterSmall)
                .foregroundStyle(count > 0 ? color : LoomColors.fgMuted)
                .monospacedDigit()
                .contentTransition(.numericText(value: Double(count)))
            Text(label)
                .font(LoomTypography.monoCaption)
                .foregroundStyle(LoomColors.fgMuted)
                .textCase(.uppercase)
        }
    }
}
