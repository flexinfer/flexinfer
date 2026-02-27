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
    public let cloudflareAccessClientID: String?
    public let cloudflareAccessClientSecret: String?

    public init(
        name: String,
        baseURL: String,
        mode: ConnectionMode,
        cloudflareAccessClientID: String? = nil,
        cloudflareAccessClientSecret: String? = nil
    ) {
        self.name = name
        self.baseURL = baseURL
        self.mode = mode
        self.cloudflareAccessClientID = cloudflareAccessClientID
        self.cloudflareAccessClientSecret = cloudflareAccessClientSecret
    }

    public var hasCloudflareAccessServiceToken: Bool {
        !(cloudflareAccessClientID ?? "").isEmpty && !(cloudflareAccessClientSecret ?? "").isEmpty
    }
}
