import SwiftUI
import LoomCompanionKit

struct AttentionLanesCard: View {
    let lanes: [DashboardAttentionLane]
    var onOpen: (DashboardAttentionLane) -> Void

    var body: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                HStack {
                    Text("Next Attention")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)
                    Spacer()
                    Text("\(lanes.count) lane\(lanes.count == 1 ? "" : "s")")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                }

                Text("Open the lane that needs action next instead of hunting through the full work surface.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)

                VStack(spacing: LoomSpacing.xs) {
                    ForEach(Array(lanes.prefix(3)), id: \.stableID) { lane in
                        Button {
                            onOpen(lane)
                        } label: {
                            HStack(alignment: .top, spacing: LoomSpacing.sm) {
                                Circle()
                                    .fill(laneColor(lane.severity))
                                    .frame(width: 10, height: 10)
                                    .padding(.top, 4)

                                VStack(alignment: .leading, spacing: 4) {
                                    HStack {
                                        Text(lane.label)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                        Spacer()
                                        Text(lane.scope)
                                            .font(LoomTypography.monoCaption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                    }

                                    Text(lane.summary.isEmpty ? fallbackSummary(for: lane) : lane.summary)
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.textSecondary)
                                        .multilineTextAlignment(.leading)
                                }

                                Image(systemName: "chevron.right")
                                    .font(.caption)
                                    .foregroundStyle(LoomColors.textTertiary)
                                    .padding(.top, 2)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, 6)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
    }

    private func laneColor(_ severity: String) -> Color {
        switch severity {
        case "critical":
            return LoomColors.statusCritical
        case "warning":
            return LoomColors.statusDegraded
        default:
            return LoomColors.statusInfo
        }
    }

    private func fallbackSummary(for lane: DashboardAttentionLane) -> String {
        switch lane.route {
        case "people":
            return "Review the people lane for the agent or session behind this pressure."
        case "connection":
            return "Open connection diagnostics for the next remediation step."
        default:
            return "Open Work for the workflow, namespace, or blocker driving this lane."
        }
    }
}
