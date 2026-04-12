import SwiftUI
import LoomCompanionKit

/// Prominent card below the attention lanes showing the single most
/// important thing the operator should do next.
struct NextActionCard: View {
    let lanes: [DashboardAttentionLane]
    let health: HealthSummary
    var onNavigate: ((DashboardView.DashboardNavAction) -> Void)?

    var body: some View {
        Button {
            HapticManager.selection()
            if let action = resolvedAction {
                onNavigate?(action.navAction)
            }
        } label: {
            LoomCard {
                HStack(spacing: LoomSpacing.sm) {
                    actionIcon
                        .font(.system(size: 22))
                        .foregroundStyle(actionColor)
                        .frame(width: 36, height: 36)

                    VStack(alignment: .leading, spacing: 2) {
                        Text(actionTitle)
                            .font(LoomTypography.bodyMedium)
                            .foregroundStyle(LoomColors.textPrimary)

                        Text(actionSubtitle)
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                            .lineLimit(2)
                    }

                    Spacer()

                    if resolvedAction != nil {
                        Image(systemName: "chevron.right")
                            .font(.caption)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                }
            }
        }
        .buttonStyle(.plain)
        .disabled(resolvedAction == nil)
    }

    // MARK: - Resolution

    private struct ResolvedAction {
        let icon: String
        let title: String
        let subtitle: String
        let color: Color
        let navAction: DashboardView.DashboardNavAction
    }

    private var resolvedAction: ResolvedAction? {
        // Priority 1: Critical severity lanes
        if let critical = lanes.first(where: { $0.severity == "critical" }) {
            return actionFromLane(critical)
        }

        // Priority 2: Degraded servers in health
        if health.degradedServers > 0 || health.downServers > 0 {
            let count = health.downServers + health.degradedServers
            return ResolvedAction(
                icon: "exclamationmark.triangle.fill",
                title: "Investigate server health",
                subtitle: "\(count) server\(count == 1 ? "" : "s") need attention",
                color: health.downServers > 0 ? LoomColors.statusCritical : LoomColors.statusDegraded,
                navAction: .connection
            )
        }

        // Priority 3: Warning severity lanes
        if let warning = lanes.first(where: { $0.severity == "warning" }) {
            return actionFromLane(warning)
        }

        // Priority 4: Any info-level lane
        if let info = lanes.first {
            return actionFromLane(info)
        }

        return nil
    }

    private func actionFromLane(_ lane: DashboardAttentionLane) -> ResolvedAction {
        let icon: String
        let title: String
        let navAction: DashboardView.DashboardNavAction

        switch lane.type {
        case "approval", "workflow_approval":
            icon = "checkmark.seal.fill"
            title = "Approve workflow step"
            navAction = navigationActionForRoute(lane.route)

        case "degraded_server", "server_health":
            icon = "exclamationmark.triangle.fill"
            title = "Investigate degraded server"
            navAction = .connection

        case "blocked_task", "blocker":
            icon = "hand.raised.fill"
            title = "Unblock stalled task"
            navAction = navigationActionForRoute(lane.route)

        case "idle_agent", "stale_heartbeat":
            icon = "person.fill.questionmark"
            title = "Check idle agent"
            navAction = .people

        case "conflict", "merge_conflict":
            icon = "arrow.triangle.merge"
            title = "Resolve merge conflict"
            navAction = navigationActionForRoute(lane.route)

        case "handoff":
            icon = "arrow.right.arrow.left"
            title = "Accept pending handoff"
            navAction = navigationActionForRoute(lane.route)

        default:
            icon = "arrow.right.circle.fill"
            title = lane.label.isEmpty ? "Review attention lane" : lane.label
            navAction = navigationActionForRoute(lane.route)
        }

        let subtitle = lane.summary.isEmpty
            ? (lane.scope.isEmpty ? "Tap to open" : lane.scope)
            : lane.summary

        let color: Color
        switch lane.severity {
        case "critical": color = LoomColors.statusCritical
        case "warning": color = LoomColors.statusDegraded
        default: color = LoomColors.statusInfo
        }

        return ResolvedAction(
            icon: icon,
            title: title,
            subtitle: subtitle,
            color: color,
            navAction: navAction
        )
    }

    private func navigationActionForRoute(_ route: String) -> DashboardView.DashboardNavAction {
        switch route {
        case "people": return .people
        case "connection": return .connection
        default: return .work
        }
    }

    // MARK: - Display Properties

    private var actionIcon: Image {
        if let action = resolvedAction {
            return Image(systemName: action.icon)
        }
        return Image(systemName: "checkmark.circle.fill")
    }

    private var actionColor: Color {
        resolvedAction?.color ?? LoomColors.statusHealthy
    }

    private var actionTitle: String {
        resolvedAction?.title ?? "All clear"
    }

    private var actionSubtitle: String {
        resolvedAction?.subtitle ?? "No items need your attention right now."
    }
}
