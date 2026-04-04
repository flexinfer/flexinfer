import Foundation

/// Matches `HealthSummary` from monitor/health.go.
public struct HealthSummary: Decodable, Sendable {
    public let totalServers: Int
    public let healthyServers: Int
    public let degradedServers: Int
    public let downServers: Int
    public let idleServers: Int

    enum CodingKeys: String, CodingKey {
        case totalServers = "total_servers"
        case healthyServers = "healthy_servers"
        case degradedServers = "degraded_servers"
        case downServers = "down_servers"
        case idleServers = "idle_servers"
    }

    public init(totalServers: Int, healthyServers: Int, degradedServers: Int, downServers: Int, idleServers: Int) {
        self.totalServers = totalServers
        self.healthyServers = healthyServers
        self.degradedServers = degradedServers
        self.downServers = downServers
        self.idleServers = idleServers
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.totalServers = try container.decodeIfPresent(Int.self, forKey: .totalServers) ?? 0
        self.healthyServers = try container.decodeIfPresent(Int.self, forKey: .healthyServers) ?? 0
        self.degradedServers = try container.decodeIfPresent(Int.self, forKey: .degradedServers) ?? 0
        self.downServers = try container.decodeIfPresent(Int.self, forKey: .downServers) ?? 0
        self.idleServers = try container.decodeIfPresent(Int.self, forKey: .idleServers) ?? 0
    }

    /// Overall health status derived from server counts.
    public var overallStatus: OverallHealthStatus {
        if downServers > 0 { return .critical }
        if degradedServers > 0 { return .degraded }
        if totalServers == 0 { return .unknown }
        return .healthy
    }
}

public enum OverallHealthStatus: String, Sendable {
    case healthy
    case degraded
    case critical
    case unknown
}
