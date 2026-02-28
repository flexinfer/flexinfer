import Foundation

/// Severity level for in-app alert notifications.
public enum AlertSeverity: String, Sendable, Comparable, Codable {
    case info
    case warning
    case critical

    public static func < (lhs: AlertSeverity, rhs: AlertSeverity) -> Bool {
        let order: [AlertSeverity] = [.info, .warning, .critical]
        return order.firstIndex(of: lhs)! < order.firstIndex(of: rhs)!
    }
}

/// Maps to iOS UNNotificationInterruptionLevel for future APNs integration.
/// Controls how prominently an alert interrupts the user.
public enum InterruptionLevel: String, Sendable, Codable, Comparable {
    /// Silently added to notification list; no sound/banner.
    case passive
    /// Default notification behavior (sound + banner).
    case active
    /// Breaks through Focus/DND for time-critical operational events.
    case timeSensitive = "time_sensitive"
    /// Reserved for emergencies requiring immediate attention (not used in v1).
    case critical

    public static func < (lhs: InterruptionLevel, rhs: InterruptionLevel) -> Bool {
        let order: [InterruptionLevel] = [.passive, .active, .timeSensitive, .critical]
        return order.firstIndex(of: lhs)! < order.firstIndex(of: rhs)!
    }
}

/// Safe navigation actions available from an alert. Restricted to read-only
/// operations to maintain v1 scope discipline.
public enum AlertAction: String, Sendable, Codable {
    /// Navigate to the related session detail screen.
    case viewSession = "view_session"
    /// Navigate to the related workflow detail screen.
    case viewWorkflow = "view_workflow"
    /// Navigate to the dashboard screen.
    case viewDashboard = "view_dashboard"
    /// Mark as acknowledged (no navigation).
    case acknowledge
}

/// A single in-app alert derived from an SSE event.
public struct AlertItem: Identifiable, Sendable {
    public let id: UUID
    public let timestamp: Date
    public let severity: AlertSeverity
    public let interruptionLevel: InterruptionLevel
    public let title: String
    public let message: String
    public let eventType: String
    public let relatedSessionId: String?
    public let relatedWorkflowId: String?
    public let allowedActions: [AlertAction]
    public var isRead: Bool

    public init(
        id: UUID = UUID(),
        timestamp: Date = Date(),
        severity: AlertSeverity,
        interruptionLevel: InterruptionLevel = .active,
        title: String,
        message: String,
        eventType: String,
        relatedSessionId: String? = nil,
        relatedWorkflowId: String? = nil,
        allowedActions: [AlertAction] = [.acknowledge],
        isRead: Bool = false
    ) {
        self.id = id
        self.timestamp = timestamp
        self.severity = severity
        self.interruptionLevel = interruptionLevel
        self.title = title
        self.message = message
        self.eventType = eventType
        self.relatedSessionId = relatedSessionId
        self.relatedWorkflowId = relatedWorkflowId
        self.allowedActions = allowedActions
        self.isRead = isRead
    }

    /// The primary quick-action for this alert (first non-acknowledge action, or acknowledge).
    public var primaryAction: AlertAction {
        allowedActions.first { $0 != .acknowledge } ?? .acknowledge
    }
}

/// Policy entry describing how an SSE event type maps to an alert.
public struct NotificationPolicyEntry: Sendable {
    public let severity: AlertSeverity
    public let interruptionLevel: InterruptionLevel
    public let titleTemplate: String
    public let allowedActions: [AlertAction]
}

/// Maps SSE event types to notification severity, interruption level, and allowed actions.
///
/// Event-to-interruption-level matrix (MBL-6):
///
/// | Event Type               | Severity | Interruption   | Actions                        |
/// |--------------------------|----------|----------------|--------------------------------|
/// | hud.health (down)        | critical | timeSensitive  | viewDashboard, acknowledge     |
/// | hud.health (degraded)    | warning  | active         | viewDashboard, acknowledge     |
/// | agent.session.reaped     | warning  | active         | viewSession, acknowledge       |
/// | hud.workflow.reject      | warning  | active         | viewWorkflow, acknowledge      |
/// | agent.session.start      | info     | passive        | viewSession, acknowledge       |
/// | agent.session.end        | info     | passive        | viewSession, acknowledge       |
/// | agent.nudge.created      | info     | passive        | acknowledge                    |
/// | hud.workflow.approve     | info     | passive        | viewWorkflow, acknowledge      |
/// | hud.handoff.created      | info     | passive        | acknowledge                    |
/// | coordinator.plan.complete| info     | passive        | acknowledge                    |
public enum NotificationPolicy {
    /// Classify an SSE event into an AlertItem, or nil if the event is not alert-worthy.
    public static func classify(event: SSEEvent) -> AlertItem? {
        let entry = policyEntry(for: event)
        guard let entry else { return nil }

        let message = buildMessage(event: event)
        let sessionId = extractSessionId(from: event.data)
        let workflowId = extractWorkflowId(from: event.data)

        return AlertItem(
            severity: entry.severity,
            interruptionLevel: entry.interruptionLevel,
            title: entry.titleTemplate,
            message: message,
            eventType: event.type,
            relatedSessionId: sessionId,
            relatedWorkflowId: workflowId,
            allowedActions: entry.allowedActions
        )
    }

    /// Look up the policy entry for a given SSE event, applying conditional logic for health events.
    public static func policyEntry(for event: SSEEvent) -> NotificationPolicyEntry? {
        switch event.type {
        case "hud.health":
            return classifyHealthEvent(data: event.data)
        case "agent.session.reaped":
            return NotificationPolicyEntry(severity: .warning, interruptionLevel: .active, titleTemplate: "Session Reaped", allowedActions: [.viewSession, .acknowledge])
        case "agent.nudge.created":
            return NotificationPolicyEntry(severity: .info, interruptionLevel: .passive, titleTemplate: "Agent Nudge Queued", allowedActions: [.acknowledge])
        case "hud.workflow.approve":
            return NotificationPolicyEntry(severity: .info, interruptionLevel: .passive, titleTemplate: "Workflow Approved", allowedActions: [.viewWorkflow, .acknowledge])
        case "hud.workflow.reject":
            return NotificationPolicyEntry(severity: .warning, interruptionLevel: .active, titleTemplate: "Workflow Rejected", allowedActions: [.viewWorkflow, .acknowledge])
        case "agent.session.start":
            return NotificationPolicyEntry(severity: .info, interruptionLevel: .passive, titleTemplate: "Session Started", allowedActions: [.viewSession, .acknowledge])
        case "agent.session.end":
            return NotificationPolicyEntry(severity: .info, interruptionLevel: .passive, titleTemplate: "Session Ended", allowedActions: [.viewSession, .acknowledge])
        case "hud.handoff.created":
            return NotificationPolicyEntry(severity: .info, interruptionLevel: .passive, titleTemplate: "Handoff Created", allowedActions: [.acknowledge])
        case "coordinator.plan.complete":
            return NotificationPolicyEntry(severity: .info, interruptionLevel: .passive, titleTemplate: "Plan Complete", allowedActions: [.acknowledge])
        default:
            return nil
        }
    }

    // MARK: - Private

    private static func classifyHealthEvent(data: String) -> NotificationPolicyEntry? {
        guard let jsonData = data.data(using: .utf8),
              let payload = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else {
            return nil
        }

        let down = payload["down_servers"] as? Int ?? 0
        let degraded = payload["degraded_servers"] as? Int ?? 0

        if down > 0 {
            return NotificationPolicyEntry(severity: .critical, interruptionLevel: .timeSensitive, titleTemplate: "Server Down", allowedActions: [.viewDashboard, .acknowledge])
        }
        if degraded > 0 {
            return NotificationPolicyEntry(severity: .warning, interruptionLevel: .active, titleTemplate: "Server Degraded", allowedActions: [.viewDashboard, .acknowledge])
        }
        return nil
    }

    private static func extractSessionId(from data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let payload = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else {
            return nil
        }
        return payload["session_id"] as? String
    }

    private static func extractWorkflowId(from data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let payload = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else {
            return nil
        }
        return payload["workflow_id"] as? String
    }

    private static func buildMessage(event: SSEEvent) -> String {
        guard let jsonData = event.data.data(using: .utf8),
              let payload = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else {
            return event.type
        }

        switch event.type {
        case "hud.health":
            let down = payload["down_servers"] as? Int ?? 0
            let degraded = payload["degraded_servers"] as? Int ?? 0
            if down > 0 { return "\(down) server(s) down" }
            if degraded > 0 { return "\(degraded) server(s) degraded" }
            return "Health update"
        case "agent.session.start":
            let agentId = payload["agent_id"] as? String ?? "unknown"
            return "Agent \(agentId) started a session"
        case "agent.session.end":
            let sessionId = payload["session_id"] as? String ?? "unknown"
            return "Session \(sessionId) ended"
        case "agent.session.reaped":
            let sessionId = payload["session_id"] as? String ?? "unknown"
            return "Session \(sessionId) was reaped due to inactivity"
        case "agent.nudge.created":
            let agentId = payload["agent_id"] as? String ?? "unknown"
            return "Nudge queued for agent \(agentId)"
        case "hud.workflow.approve":
            let workflowId = payload["workflow_id"] as? String ?? "unknown"
            return "Workflow \(workflowId) approved"
        case "hud.workflow.reject":
            let workflowId = payload["workflow_id"] as? String ?? "unknown"
            return "Workflow \(workflowId) rejected"
        case "hud.handoff.created":
            let from = payload["from_agent"] as? String ?? "unknown"
            let to = payload["to_agent"] as? String ?? "unknown"
            return "Handoff from \(from) to \(to)"
        case "coordinator.plan.complete":
            let planId = payload["plan_id"] as? String ?? payload["session_id"] as? String ?? "unknown"
            return "Plan \(planId) completed"
        default:
            return event.type
        }
    }
}
