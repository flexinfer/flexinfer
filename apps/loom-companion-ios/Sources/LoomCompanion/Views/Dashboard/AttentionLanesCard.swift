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
        let display = displayFields(for: lane)

        LoomListRow(
            accentColor: laneColor(lane.severity),
            title: display.title,
            subtitle: display.subtitle,
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
                    if let metric = display.trailingMetric {
                        Text(metric)
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

    /// Collapses the raw lane fields into display values that avoid duplication
    /// across title / subtitle / trailing. See the hero card for the same idea.
    private func displayFields(for lane: DashboardAttentionLane) -> (title: String, subtitle: String?, trailingMetric: String?) {
        let labelIsGeneric = isGenericLaneLabel(lane.label)

        let title: String
        if !labelIsGeneric {
            title = lane.label
        } else if !lane.summary.isEmpty {
            title = lane.summary.prefix(1).uppercased() + lane.summary.dropFirst()
        } else {
            title = fallbackTitle(lane)
        }

        // Subtitle falls back to the scope only when the summary wasn't
        // consumed by the title. Otherwise drop it so the row is clean.
        let subtitle: String?
        if labelIsGeneric, !lane.summary.isEmpty {
            // summary was consumed by title
            subtitle = lane.scope.isEmpty ? nil : lane.scope
        } else if !lane.summary.isEmpty {
            subtitle = lane.summary
        } else {
            subtitle = fallbackSummary(for: lane)
        }

        // Only show a trailing metric when it adds signal — we dedupe with
        // the subtitle so path/scope doesn't appear twice in the same row.
        let trailingMetric: String?
        if labelIsGeneric, !lane.summary.isEmpty {
            // Title took the summary, subtitle took the scope — nothing left.
            trailingMetric = nil
        } else if !lane.scope.isEmpty {
            trailingMetric = lane.scope
        } else {
            trailingMetric = nil
        }

        return (title, subtitle, trailingMetric)
    }

    private func isGenericLaneLabel(_ label: String) -> Bool {
        let trimmed = label.trimmingCharacters(in: .whitespaces).lowercased()
        if trimmed.isEmpty { return true }
        if trimmed.hasSuffix(" lane") || trimmed == "lane" { return true }
        return false
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
