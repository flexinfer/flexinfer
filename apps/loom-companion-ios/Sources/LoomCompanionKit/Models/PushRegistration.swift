import Foundation

/// Request body for POST /api/mobile/v1/push/register.
public struct PushRegistrationRequest: Codable, Sendable {
    public let token: String
    public let platform: PushPlatform

    public init(token: String, platform: PushPlatform) {
        self.token = token
        self.platform = platform
    }
}

/// Push notification platform identifier.
public enum PushPlatform: String, Codable, Sendable {
    case apns
    case fcm
}

/// Response from POST /api/mobile/v1/push/register.
public struct PushRegistrationResponse: Codable, Sendable {
    public let registered: Bool
    public let registrationId: String

    public init(registered: Bool, registrationId: String) {
        self.registered = registered
        self.registrationId = registrationId
    }

    enum CodingKeys: String, CodingKey {
        case registered
        case registrationId = "registration_id"
    }
}

/// Request body for POST /api/mobile/v1/push/unregister.
public struct PushUnregisterRequest: Codable, Sendable {
    public let token: String

    public init(token: String) {
        self.token = token
    }
}

/// Response from POST /api/mobile/v1/push/unregister.
public struct PushUnregisterResponse: Codable, Sendable {
    public let removed: Bool

    public init(removed: Bool) {
        self.removed = removed
    }
}
