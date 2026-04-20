import SwiftUI
import LoomCompanionKit

/// Shows the attention queue *after* the hero NextActionCard.
///
/// The hero already anchors the single most urgent lane. This card exists to
/// let the operator triage the rest of the queue without drowning in repetition
/// — so rather than listing every lane individually, it **groups lanes by
/// type** and surfaces aggregates:
///
///   ─ Blocked tasks         3 lanes · 26 tasks across services/loom-core …
///   ─ Pending approvals     2 lanes · platform/gitops
///   ─ Idle agents           1 lane  · loom-companion-ios
///
/// A group of one collapses to a normal single-lane row (no synthetic "1 lane"
/// wrapping). Groups of many show the aggregate up front and can be tapped to
/// drill into the first lane (or, when the API supports it, a filtered list).
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

    // MARK: - Grouping

    /// Lanes this card should render, after `skipFirst` is applied.
    private var visibleLanes: [DashboardAttentionLane] {
        skipFirst ? Array(lanes.dropFirst()) : lanes
    }

    /// Group the visible lanes by type, preserving first-seen order.
    /// Unknown/empty type falls into a synthetic `"_other"` bucket.
    private var groups: [AttentionGroup] {
        var byKey: [String: [DashboardAttentionLane]] = [:]
        var order: [String] = []
        for lane in visibleLanes {
            let key = lane.type.isEmpty ? "_other" : lane.type
            if byKey[key] == nil { order.append(key) }
            byKey[key, default: []].append(lane)
        }
        return order.compactMap { key in
            byKey[key].flatMap { lanes in lanes.isEmpty ? nil : AttentionGroup(typeKey: key, lanes: lanes) }
        }
    }

    private var hasCritical: Bool {
        visibleLanes.contains { $0.severity == "critical" }
    }

    // MARK: - Body

    var body: some View {
        LoomCard(priority: .standard) {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                header
                ForEach(Array(groups.enumerated()), id: \.element.id) { index, group in
                    if group.lanes.count == 1 {
                        singleLaneRow(group.lanes[0])
                    } else {
                        groupRow(group)
                    }
                    if index < groups.count - 1 {
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

    // MARK: - Grouped row (multi-lane bucket)

    /// Collapsed row showing an aggregate of N lanes of the same type.
    /// Tap opens the highest-severity lane in the group so the operator lands
    /// on the worst offender first.
    @ViewBuilder
    private func groupRow(_ group: AttentionGroup) -> some View {
        let pilotLane = group.pilotLane
        Button {
            HapticManager.selection()
            onOpen(pilotLane)
        } label: {
            LoomListRow(
                accentColor: laneColor(group.severity),
                title: group.aggregateTitle,
                subtitle: group.aggregateSubtitle,
                isLive: group.severity == "critical",
                emphasizeTitle: group.severity == "critical",
                leading: {
                    LoomRowIcon(
                        systemName: laneIcon(group.typeKey),
                        color: laneColor(group.severity),
                        size: 11
                    )
                },
                trailing: {
                    HStack(spacing: LoomSpacing.xxs) {
                        Text("\(group.lanes.count)")
                            .font(LoomTypography.monoMedium)
                            .foregroundStyle(laneColor(group.severity))
                        Text("lanes")
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.fgMuted)
                        Image(systemName: "chevron.right")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(LoomColors.fgMuted)
                    }
                }
            )
        }
        .buttonStyle(.plain)
    }

    // MARK: - Single-lane row (bucket of one)

    /// Fall-through for groups where aggregation adds no value — just show
    /// the lane as itself. Reuses the display-field composition from the
    /// previous iteration so repeated generic labels still collapse.
    @ViewBuilder
    private func singleLaneRow(_ lane: DashboardAttentionLane) -> some View {
        let display = displayFields(for: lane)
        Button {
            HapticManager.selection()
            onOpen(lane)
        } label: {
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
        .buttonStyle(.plain)
    }

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
        let subtitle: String?
        if labelIsGeneric, !lane.summary.isEmpty {
            subtitle = lane.scope.isEmpty ? nil : lane.scope
        } else if !lane.summary.isEmpty {
            subtitle = lane.summary
        } else {
            subtitle = fallbackSummary(for: lane)
        }
        let trailingMetric: String?
        if labelIsGeneric, !lane.summary.isEmpty {
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

    // MARK: - Color / icon / fallback helpers

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
        case "_other": return "ellipsis.circle"
        default: return "flag.fill"
        }
    }

    private func fallbackTitle(_ lane: DashboardAttentionLane) -> String {
        AttentionGroup.typeTitle(for: lane.type)
    }

    private func fallbackSummary(for lane: DashboardAttentionLane) -> String {
        switch lane.route {
        case "people": return "Review the agents lane for the agent or session behind this pressure."
        case "connection": return "Open connection diagnostics for the next remediation step."
        default: return "Open Work for the workflow, namespace, or blocker driving this lane."
        }
    }
}

// MARK: - AttentionGroup

/// Aggregate view over lanes of the same type. Produces a scannable headline
/// ("Blocked tasks · 3 lanes across services/loom-core, platform/gitops") that
/// collapses repetition without hiding volume.
private struct AttentionGroup: Identifiable {
    let typeKey: String
    let lanes: [DashboardAttentionLane]

    var id: String { typeKey }

    /// Highest-severity lane goes first on drill-in — operator lands on the
    /// worst case, not an arbitrary one.
    var pilotLane: DashboardAttentionLane {
        let order: [String: Int] = ["critical": 0, "warning": 1, "info": 2]
        return lanes.min { (a, b) in
            (order[a.severity] ?? 3) < (order[b.severity] ?? 3)
        } ?? lanes[0]
    }

    var severity: String {
        if lanes.contains(where: { $0.severity == "critical" }) { return "critical" }
        if lanes.contains(where: { $0.severity == "warning" }) { return "warning" }
        return "info"
    }

    /// Title like "Blocked tasks" or a capitalized summary when type is unknown.
    var aggregateTitle: String {
        if let typed = Self.typeTitleIfKnown(for: typeKey) {
            return typed
        }
        // Unknown type — use the first non-empty summary, capitalized.
        if let summary = lanes.first(where: { !$0.summary.isEmpty })?.summary {
            return summary.prefix(1).uppercased() + summary.dropFirst()
        }
        return "Attention lanes"
    }

    /// "3 lanes · services/loom-core, platform/gitops (+2)" — shows volume and
    /// the top few distinct scopes so the operator knows where to look.
    var aggregateSubtitle: String? {
        let distinctScopes = lanes
            .map { $0.scope }
            .filter { !$0.isEmpty }
            .reduce(into: [String]()) { acc, scope in
                if !acc.contains(scope) { acc.append(scope) }
            }

        if distinctScopes.isEmpty {
            return "\(lanes.count) lane\(lanes.count == 1 ? "" : "s")"
        }

        let preview = distinctScopes.prefix(2).joined(separator: ", ")
        let remainder = distinctScopes.count - 2
        if remainder > 0 {
            return "\(preview) (+\(remainder))"
        }
        return preview
    }

    // MARK: - Type labels (plural, aggregate-form)

    static func typeTitle(for type: String) -> String {
        typeTitleIfKnown(for: type) ?? "Attention lane"
    }

    static func typeTitleIfKnown(for type: String) -> String? {
        switch type {
        case "approval", "workflow_approval": return "Pending approvals"
        case "degraded_server", "server_health": return "Degraded servers"
        case "blocked_task", "blocker": return "Blocked tasks"
        case "idle_agent", "stale_heartbeat": return "Idle agents"
        case "conflict", "merge_conflict": return "Merge conflicts"
        case "handoff": return "Pending handoffs"
        default: return nil
        }
    }
}
