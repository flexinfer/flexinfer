import SwiftUI
import LoomCompanionKit

/// Shows the attention queue *after* the hero NextActionCard. The top lane is
/// already represented by the hero, so this card lists the remainder so the
/// operator can triage the full queue without hunting.
///
/// Rows use the shared LoomListRow language so hierarchy reads identically
/// from dashboard down through drill-in surfaces.
struct AttentionLanesCard: View {
    let lanes: [DashboardAttentionLane]
    let skipFirst: Bool
    var onOpen: (DashboardAttentionLane) -> Void

    init(
        lanes: [DashboardAttentionLane],
        skipFirst: Bool = false,
        onOpen: @escaping (DashboardAttentionLane) -> Void
    ) {
        self.lanes = lanes
        self.skipFirst = skipFirst
        self.onOpen = onOpen
    }

    private var displayedLanes: [DashboardAttentionLane] {
        let source = skipFirst ? Array(lanes.dropFirst()) : lanes
        return Array(source.prefix(5))
    }

    private var hasCritical: Bool {
        displayedLanes.contains { $0.severity == "critical" }
    }

    var body: some View {
        LoomCard(priority: .standard) {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                header
                ForEach(Array(displayedLanes.enumerated()), id: \.element.stableID) { index, lane in
                    Button {
                        HapticManager.selection()
                        onOpen(lane)
                    } label: {
                        laneRow(lane)
                    }
                    .buttonStyle(.plain)
                    if index < displayedLanes.count - 1 {
                        Divider().overlay(LoomColors.border)
                    }
                }
            }
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: LoomSpacing.xs) {
            Text("Attention Queue")
                .font(LoomTypography.headlineMedium)
                .foregroundStyle(LoomColors.fgPrimary)
            if hasCritical {
                Circle()
                    .fill(LoomColors.statusCritical)
                    .frame(width: 6, height: 6)
                    .pulse()
            }
            Spacer()
            Text("\(lanes.count)")
                .font(LoomTypography.monoMedium)
                .foregroundStyle(LoomColors.fgSecondary)
            Text(lanes.count == 1 ? "lane" : "lanes")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.fgMuted)
        }
    }

    // MARK: - Lane Row (LoomListRow-compatible visual language)

    @ViewBuilder
    private func laneRow(_ lane: DashboardAttentionLane) -> some View {
        LoomListRow(
            accentColor: laneColor(lane.severity),
            title: lane.label.isEmpty ? fallbackTitle(lane) : lane.label,
            subtitle: lane.summary.isEmpty ? fallbackSummary(for: lane) : lane.summary,
            isLive: lane.severity == "critical",
            emphasizeTitle: lane.severity == "critical",
            leading: {
                LoomRowIcon(
                    systemName: laneIcon(lane.type),
                    color: laneColor(lane.severity),
                    size: 11
                )
            },
            trailing: {
                HStack(spacing: LoomSpacing.xxs) {
                    if !lane.scope.isEmpty {
                        Text(lane.scope)
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.fgMuted)
                            .lineLimit(1)
                    }
                    Image(systemName: "chevron.right")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(LoomColors.fgMuted)
                }
            }
        )
    }

    // MARK: - Helpers

    private func laneColor(_ severity: String) -> Color {
        switch severity {
        case "critical": return LoomColors.statusCritical
        case "warning": return LoomColors.statusDegraded
        default: return LoomColors.statusInfo
        }
    }

    private func laneIcon(_ type: String) -> String {
        switch type {
        case "approval", "workflow_approval": return "checkmark.seal"
        case "degraded_server", "server_health": return "exclamationmark.triangle"
        case "blocked_task", "blocker": return "hand.raised"
        case "idle_agent", "stale_heartbeat": return "person.fill.questionmark"
        case "conflict", "merge_conflict": return "arrow.triangle.merge"
        case "handoff": return "arrow.right.arrow.left"
        default: return "flag.fill"
        }
    }

    private func fallbackTitle(_ lane: DashboardAttentionLane) -> String {
        switch lane.type {
        case "approval", "workflow_approval": return "Workflow needs approval"
        case "degraded_server", "server_health": return "Degraded server"
        case "blocked_task", "blocker": return "Blocked task"
        case "idle_agent", "stale_heartbeat": return "Idle agent"
        case "conflict", "merge_conflict": return "Merge conflict"
        case "handoff": return "Pending handoff"
        default: return "Attention required"
        }
    }

    private func fallbackSummary(for lane: DashboardAttentionLane) -> String {
        switch lane.route {
        case "people": return "Review the people lane for the agent or session behind this pressure."
        case "connection": return "Open connection diagnostics for the next remediation step."
        default: return "Open Work for the workflow, namespace, or blocker driving this lane."
        }
    }
}
