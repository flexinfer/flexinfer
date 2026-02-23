import Foundation

/// Severity level for in-app alert notifications.
public enum AlertSeverity: String, Sendable, Comparable {
    case info
    case warning
    case critical

    public static func < (lhs: AlertSeverity, rhs: AlertSeverity) -> Bool {
        let order: [AlertSeverity] = [.info, .warning, .critical]
        return order.firstIndex(of: lhs)! < order.firstIndex(of: rhs)!
    }
}

/// A single in-app alert derived from an SSE event.
public struct AlertItem: Identifiable, Sendable {
    public let id: UUID
    public let timestamp: Date
    public let severity: AlertSeverity
    public let title: String
    public let message: String
    public let eventType: String
    public let relatedSessionId: String?
    public var isRead: Bool

    public init(
        id: UUID = UUID(),
        timestamp: Date = Date(),
        severity: AlertSeverity,
        title: String,
        message: String,
        eventType: String,
        relatedSessionId: String? = nil,
        isRead: Bool = false
    ) {
        self.id = id
        self.timestamp = timestamp
        self.severity = severity
        self.title = title
        self.message = message
        self.eventType = eventType
        self.relatedSessionId = relatedSessionId
        self.isRead = isRead
    }
}

/// Policy entry describing how an SSE event type maps to an alert.
public struct NotificationPolicyEntry: Sendable {
    public let severity: AlertSeverity
    public let titleTemplate: String
}

/// Maps SSE event types to notification severity and title templates.
public enum NotificationPolicy {
    /// Classify an SSE event into an AlertItem, or nil if the event is not alert-worthy.
    public static func classify(event: SSEEvent) -> AlertItem? {
        let entry = policyEntry(for: event)
        guard let entry else { return nil }

        let message = buildMessage(event: event)
        let sessionId = extractSessionId(from: event.data)

        return AlertItem(
            severity: entry.severity,
            title: entry.titleTemplate,
            message: message,
            eventType: event.type,
            relatedSessionId: sessionId
        )
    }

    /// Look up the policy entry for a given SSE event, applying conditional logic for health events.
    public static func policyEntry(for event: SSEEvent) -> NotificationPolicyEntry? {
        switch event.type {
        case "hud.health":
            return classifyHealthEvent(data: event.data)
        case "agent.session.reaped":
            return NotificationPolicyEntry(severity: .warning, titleTemplate: "Session Reaped")
        case "agent.nudge.created":
            return NotificationPolicyEntry(severity: .info, titleTemplate: "Agent Nudge Queued")
        case "hud.workflow.approve":
            return NotificationPolicyEntry(severity: .info, titleTemplate: "Workflow Approved")
        case "hud.workflow.reject":
            return NotificationPolicyEntry(severity: .warning, titleTemplate: "Workflow Rejected")
        case "agent.session.start":
            return NotificationPolicyEntry(severity: .info, titleTemplate: "Session Started")
        case "agent.session.end":
            return NotificationPolicyEntry(severity: .info, titleTemplate: "Session Ended")
        case "hud.handoff.created":
            return NotificationPolicyEntry(severity: .info, titleTemplate: "Handoff Created")
        case "coordinator.plan.complete":
            return NotificationPolicyEntry(severity: .info, titleTemplate: "Plan Complete")
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
            return NotificationPolicyEntry(severity: .critical, titleTemplate: "Server Down")
        }
        if degraded > 0 {
            return NotificationPolicyEntry(severity: .warning, titleTemplate: "Server Degraded")
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
