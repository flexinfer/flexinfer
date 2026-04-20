import SwiftUI
import LoomCompanionKit

/// Hero card — the single most important thing the operator should do next.
/// Scales up dramatically when critical/warning, recedes to a calm "all clear"
/// state when nothing needs attention. This is the top-of-dashboard anchor.
struct NextActionCard: View {
    let lanes: [DashboardAttentionLane]
    let health: HealthSummary
    /// Critical-severity alerts are folded into this card (priority #1) instead
    /// of a separate red banner — the dashboard only shouts in one place.
    let criticalAlerts: [AlertItem]
    var onNavigate: ((DashboardView.DashboardNavAction) -> Void)?

    init(
        lanes: [DashboardAttentionLane],
        health: HealthSummary,
        criticalAlerts: [AlertItem] = [],
        onNavigate: ((DashboardView.DashboardNavAction) -> Void)? = nil
    ) {
        self.lanes = lanes
        self.health = health
        self.criticalAlerts = criticalAlerts
        self.onNavigate = onNavigate
    }

    // MARK: - Body

    var body: some View {
        Button {
            guard resolvedAction != nil else { return }
            HapticManager.selection()
            if let action = resolvedAction {
                onNavigate?(action.navAction)
            }
        } label: {
            LoomCard(priority: cardPriority, accent: cardAccent) {
                if resolvedAction != nil {
                    activeLayout
                } else {
                    allClearLayout
                }
            }
        }
        .buttonStyle(.plain)
        .disabled(resolvedAction == nil)
    }

    // MARK: - Active Layout (action needed)

    @ViewBuilder
    private var activeLayout: some View {
        let action = resolvedAction!
        VStack(alignment: .leading, spacing: LoomSpacing.md) {
            // Eyebrow — severity label that telegraphs the card's purpose
            HStack(spacing: LoomSpacing.xs) {
                Circle()
                    .fill(action.color)
                    .frame(width: 7, height: 7)
                Text(eyebrow(for: action).uppercased())
                    .font(LoomTypography.sectionTitle)
                    .foregroundStyle(action.color)
                    .tracking(1.2)
                Spacer()
                Image(systemName: action.icon)
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(action.color)
            }

            // Primary headline — hero-sized, the ONE thing to do
            Text(action.title)
                .font(LoomTypography.headlineLarge)
                .foregroundStyle(LoomColors.fgPrimary)
                .lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)

            // Secondary context
            if !action.subtitle.isEmpty {
                Text(action.subtitle)
                    .font(LoomTypography.bodyRegular)
                    .foregroundStyle(LoomColors.fgSecondary)
                    .lineLimit(2)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }

            // CTA footer — routes user to the right surface
            HStack(spacing: LoomSpacing.xs) {
                Text(ctaLabel(for: action))
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(action.color)
                Image(systemName: "arrow.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(action.color)
                Spacer()
                if lanes.count > 1 {
                    Text("+\(lanes.count - 1) more lane\(lanes.count - 1 == 1 ? "" : "s")")
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.fgMuted)
                }
            }
        }
    }

    // MARK: - All-Clear Layout (calm state)

    @ViewBuilder
    private var allClearLayout: some View {
        HStack(spacing: LoomSpacing.md) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 28, weight: .semibold))
                .foregroundStyle(LoomColors.statusHealthy)
                .frame(width: 40, height: 40)

            VStack(alignment: .leading, spacing: 2) {
                Text("All clear")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.fgPrimary)
                Text("No items need your attention.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.fgSecondary)
            }
            Spacer()
        }
    }

    // MARK: - Priority & Accent

    /// Critical → hero. Warning → hero (no pulse). Info only → standard (no eyebrow shouting).
    private var cardPriority: LoomCardPriority {
        guard let action = resolvedAction else { return .compact }
        switch action.severity {
        case .critical, .warning: return .hero
        case .info: return .standard
        }
    }

    private var cardAccent: LoomCardAccent {
        guard let action = resolvedAction else { return .none }
        return .severity(action.color, pulse: action.severity == .critical)
    }

    private func eyebrow(for action: ResolvedAction) -> String {
        switch action.severity {
        case .critical: return "Critical · Do Next"
        case .warning: return "Attention · Do Next"
        case .info: return "Do Next"
        }
    }

    private func ctaLabel(for action: ResolvedAction) -> String {
        switch action.navAction {
        case .people: return "Open Agents"
        case .work: return "Open Work"
        case .connection: return "Open Connection"
        case .liveActivities: return "View Live"
        case .alerts: return "View Alerts"
        }
    }

    // MARK: - Resolution

    private enum Severity { case critical, warning, info }

    private struct ResolvedAction {
        let icon: String
        let title: String
        let subtitle: String
        let color: Color
        let severity: Severity
        let navAction: DashboardView.DashboardNavAction
    }

    private var resolvedAction: ResolvedAction? {
        // Priority 0 — Unread critical alerts. These used to surface as a separate
        // red banner at the top of the dashboard; folded here so the dashboard
        // has exactly one hero at any moment.
        let unreadCritical = criticalAlerts.filter { !$0.isRead }
        if let alert = unreadCritical.first {
            let countSuffix = unreadCritical.count > 1 ? " (+\(unreadCritical.count - 1) more)" : ""
            return ResolvedAction(
                icon: "exclamationmark.triangle.fill",
                title: "\(unreadCritical.count) critical alert\(unreadCritical.count == 1 ? "" : "s")\(countSuffix)",
                subtitle: alert.message,
                color: LoomColors.statusCritical,
                severity: .critical,
                navAction: .alerts
            )
        }

        // Priority 1 — Critical severity lanes
        if let critical = lanes.first(where: { $0.severity == "critical" }) {
            return actionFromLane(critical, severity: .critical)
        }
        // Priority 2 — Degraded/down servers in health
        if health.downServers > 0 || health.degradedServers > 0 {
            let count = health.downServers + health.degradedServers
            let isDown = health.downServers > 0
            return ResolvedAction(
                icon: "exclamationmark.triangle.fill",
                title: isDown
                    ? "\(health.downServers) server\(health.downServers == 1 ? "" : "s") down — investigate"
                    : "\(count) server\(count == 1 ? "" : "s") degraded",
                subtitle: "Open connection diagnostics for the next remediation step.",
                color: isDown ? LoomColors.statusCritical : LoomColors.statusDegraded,
                severity: isDown ? .critical : .warning,
                navAction: .connection
            )
        }
        // Priority 3 — Warning severity lanes
        if let warning = lanes.first(where: { $0.severity == "warning" }) {
            return actionFromLane(warning, severity: .warning)
        }
        // Priority 4 — Info-level lane
        if let info = lanes.first {
            return actionFromLane(info, severity: .info)
        }
        return nil
    }

    private func actionFromLane(_ lane: DashboardAttentionLane, severity: Severity) -> ResolvedAction {
        let (icon, titleFallback, navAction) = laneMeta(for: lane)

        // Backend often sends a category label (`"Agent lane"`, `"Work lane"`)
        // plus a generic summary (`"blocked tasks"`). That reads as noise in
        // the hero slot — compose a descriptive title from whatever signal
        // we actually have and push the rest down to the subtitle.
        let labelIsGeneric = isGenericLaneLabel(lane.label)
        let typeIsKnown = isTypedLane(lane.type)

        let title: String
        if !labelIsGeneric {
            title = lane.label
        } else if typeIsKnown {
            title = titleFallback
        } else if !lane.summary.isEmpty {
            // Capitalize so "blocked tasks" reads as a headline.
            title = lane.summary.prefix(1).uppercased() + lane.summary.dropFirst()
        } else {
            title = titleFallback
        }

        let subtitle = composeSubtitle(lane: lane, titleConsumedSummary: labelIsGeneric && !typeIsKnown && !lane.summary.isEmpty)

        let color: Color
        switch severity {
        case .critical: color = LoomColors.statusCritical
        case .warning: color = LoomColors.statusDegraded
        case .info: color = LoomColors.statusInfo
        }
        return ResolvedAction(
            icon: icon,
            title: title,
            subtitle: subtitle,
            color: color,
            severity: severity,
            navAction: navAction
        )
    }

    /// Detects labels that are too generic to be useful as a hero title.
    /// These are category names (e.g. "Agent lane", "Work lane") rather than
    /// action-oriented descriptions.
    private func isGenericLaneLabel(_ label: String) -> Bool {
        let trimmed = label.trimmingCharacters(in: .whitespaces).lowercased()
        if trimmed.isEmpty { return true }
        // Matches "agent lane", "work lane", "system lane", etc.
        if trimmed.hasSuffix(" lane") || trimmed == "lane" { return true }
        return false
    }

    /// Whether `lane.type` maps to one of the known descriptive fallbacks in
    /// `laneMeta`. Unknown types fall through to the generic "Review" title
    /// which we avoid when we can compose something better from `summary`.
    private func isTypedLane(_ type: String) -> Bool {
        switch type {
        case "approval", "workflow_approval",
             "degraded_server", "server_health",
             "blocked_task", "blocker",
             "idle_agent", "stale_heartbeat",
             "conflict", "merge_conflict",
             "handoff":
            return true
        default:
            return false
        }
    }

    /// Builds the hero subtitle. Scope (path/namespace) is the most actionable
    /// context after the title; include it when present. If the summary was
    /// already consumed by the title, drop it here to avoid duplication.
    private func composeSubtitle(lane: DashboardAttentionLane, titleConsumedSummary: Bool) -> String {
        var parts: [String] = []
        if !titleConsumedSummary, !lane.summary.isEmpty {
            parts.append(lane.summary)
        }
        if !lane.scope.isEmpty {
            parts.append(lane.scope)
        }
        if parts.isEmpty {
            return "Tap to open"
        }
        return parts.joined(separator: " · ")
    }

    private func laneMeta(for lane: DashboardAttentionLane)
        -> (icon: String, title: String, nav: DashboardView.DashboardNavAction)
    {
        // Derived route. The backend `route` hint is a sensible default, but
        // we override it when the *type* has a stronger affinity to a
        // specific surface — e.g. a blocked task should route to Work even
        // if the backend tagged the lane as `people`.
        let backendRoute: DashboardView.DashboardNavAction = {
            switch lane.route {
            case "people": return .people
            case "connection": return .connection
            default: return .work
            }
        }()
        switch lane.type {
        case "approval", "workflow_approval":
            return ("checkmark.seal.fill", "Approve workflow step", .work)
        case "degraded_server", "server_health":
            return ("exclamationmark.triangle.fill", "Investigate degraded server", .connection)
        case "blocked_task", "blocker":
            // Task work lives in the Work tab.
            return ("hand.raised.fill", "Unblock stalled task", .work)
        case "idle_agent", "stale_heartbeat":
            return ("person.fill.questionmark", "Check idle agent", .people)
        case "conflict", "merge_conflict":
            return ("arrow.triangle.merge", "Resolve merge conflict", .work)
        case "handoff":
            return ("arrow.right.arrow.left", "Accept pending handoff", .work)
        default:
            return ("flag.fill", "Review attention lane", backendRoute)
        }
    }
}
