import Foundation

/// Response from GET /api/mobile/v1/alerts/policy.
public struct MobileAlertPolicyResponse: Codable, Sendable {
    public let policy: [MobileAlertPolicyEntry]
    public let version: String

    public init(policy: [MobileAlertPolicyEntry], version: String) {
        self.policy = policy
        self.version = version
    }
}

/// A single event-policy row from the server policy matrix.
public struct MobileAlertPolicyEntry: Codable, Sendable, Identifiable {
    public let eventType: String
    public let severity: String
    public let interruptionLevel: String
    public let title: String
    public let allowedActions: [String]
    public let conditional: Bool

    public var id: String {
        "\(eventType)|\(severity)|\(interruptionLevel)|\(title)"
    }

    public init(
        eventType: String,
        severity: String,
        interruptionLevel: String,
        title: String,
        allowedActions: [String],
        conditional: Bool
    ) {
        self.eventType = eventType
        self.severity = severity
        self.interruptionLevel = interruptionLevel
        self.title = title
        self.allowedActions = allowedActions
        self.conditional = conditional
    }

    enum CodingKeys: String, CodingKey {
        case eventType = "event_type"
        case severity
        case interruptionLevel = "interruption_level"
        case title
        case allowedActions = "allowed_actions"
        case conditional
    }
}
