import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("DashboardData Decoding")
struct DashboardDataTests {

    @Test("Decodes full dashboard response")
    func decodesDashboard() throws {
        let data = try loadFixture("dashboard_response")
        let envelope = try JSONDecoder().decode(APIEnvelope<DashboardData>.self, from: data)
        #expect(envelope.ok == true)

        let dash = try #require(envelope.data)
        #expect(dash.daemonRunning == true)
        #expect(dash.serverCount == 5)
        #expect(dash.activeSessions == 2)
        #expect(dash.activeAgents == 3)
        #expect(dash.idleAgents == 1)
        #expect(dash.offlineAgents == 0)
        #expect(dash.updatedAt == "2026-02-23T12:00:00Z")
    }

    @Test("Decodes health summary")
    func decodesHealthSummary() throws {
        let data = try loadFixture("dashboard_response")
        let envelope = try JSONDecoder().decode(APIEnvelope<DashboardData>.self, from: data)
        let health = try #require(envelope.data?.health)

        #expect(health.totalServers == 5)
        #expect(health.healthyServers == 4)
        #expect(health.degradedServers == 1)
        #expect(health.downServers == 0)
        #expect(health.idleServers == 0)
        #expect(health.overallStatus == .degraded)
    }

    @Test("Decodes recent timeline entries")
    func decodesTimeline() throws {
        let data = try loadFixture("dashboard_response")
        let envelope = try JSONDecoder().decode(APIEnvelope<DashboardData>.self, from: data)
        let timeline = try #require(envelope.data?.recentTimeline)

        #expect(timeline.count == 2)
        #expect(timeline[0].eventType == "agent.session.start")
        #expect(timeline[0].agentId == "claude-code")
        #expect(timeline[1].eventType == "agent.heartbeat")
    }

    @Test("Decodes heartbeat summary")
    func decodesHeartbeatSummary() throws {
        let json = """
        {
          "ok": true,
          "data": {
            "daemon_running": true,
            "server_count": 5,
            "active_sessions": 2,
            "active_agents": 3,
            "idle_agents": 1,
            "offline_agents": 0,
            "updated_at": "2026-02-23T12:00:00Z",
            "health": {
              "total_servers": 5,
              "healthy_servers": 4,
              "degraded_servers": 1,
              "down_servers": 0,
              "idle_servers": 0
            },
            "recent_timeline": [],
            "last_heartbeat": {
              "agent_id": "claude-code",
              "timestamp": "2026-02-23T11:59:30Z",
              "count_1h": 12
            }
          }
        }
        """

        let envelope = try JSONDecoder().decode(APIEnvelope<DashboardData>.self, from: Data(json.utf8))
        let heartbeat = try #require(envelope.data?.lastHeartbeat)
        #expect(heartbeat.agentId == "claude-code")
        #expect(heartbeat.count1h == 12)
    }

    @Test("Health status healthy when no degraded or down")
    func healthStatusHealthy() {
        let health = HealthSummary(
            totalServers: 3, healthyServers: 3,
            degradedServers: 0, downServers: 0, idleServers: 0
        )
        #expect(health.overallStatus == .healthy)
    }

    @Test("Health status critical when servers down")
    func healthStatusCritical() {
        let health = HealthSummary(
            totalServers: 3, healthyServers: 1,
            degradedServers: 0, downServers: 2, idleServers: 0
        )
        #expect(health.overallStatus == .critical)
    }

    @Test("Health status unknown when no servers")
    func healthStatusUnknown() {
        let health = HealthSummary(
            totalServers: 0, healthyServers: 0,
            degradedServers: 0, downServers: 0, idleServers: 0
        )
        #expect(health.overallStatus == .unknown)
    }
}
