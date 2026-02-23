import Foundation

/// Connectivity mode for the Loom HUD connection.
public enum ConnectionMode: String, Codable, Sendable {
    case lan
    case gateway
}

/// A saved connection profile for a Loom HUD instance.
public struct ConnectionProfile: Codable, Identifiable, Sendable {
    public var id: String { name }

    public let name: String
    public let baseURL: String
    public let mode: ConnectionMode

    public init(name: String, baseURL: String, mode: ConnectionMode) {
        self.name = name
        self.baseURL = baseURL
        self.mode = mode
    }
}
