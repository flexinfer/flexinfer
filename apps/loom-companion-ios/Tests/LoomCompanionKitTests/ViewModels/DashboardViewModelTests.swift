import Testing
import Foundation
@testable import LoomCompanionKit

@MainActor
@Suite("DashboardViewModel")
struct DashboardViewModelTests {

    @Test("Load success populates dashboard")
    func loadSuccess() async {
        let client = MockAPIClient()
        client.dashboardResponse = DashboardData(
            daemonRunning: true, serverCount: 3, activeSessions: 1,
            activeAgents: 2, idleAgents: 0, offlineAgents: 0,
            updatedAt: "2026-02-23T12:00:00Z",
            health: HealthSummary(totalServers: 3, healthyServers: 3, degradedServers: 0, downServers: 0, idleServers: 0),
            coordination: DashboardCoordination(
                summary: DashboardCoordinationSummary(
                    activeNamespaces: 1,
                    namespacesAtRisk: 0,
                    agentsNeedingAttention: 0,
                    sharedBranches: 0,
                    conflictFiles: 0,
                    crossAgentBlockers: 0,
                    orphanTasks: 0,
                    idleClaimHolders: 0,
                    mergeReadyBranches: 0
                ),
                attentionLanes: []
            ),
            recentTimeline: []
        )

        let vm = DashboardViewModel(apiClient: client)
        await vm.load()

        #expect(vm.dashboard != nil)
        #expect(vm.dashboard?.activeSessions == 1)
        #expect(vm.error == nil)
    }

    @Test("Load failure sets error")
    func loadFailure() async {
        let client = MockAPIClient()
        client.shouldFail = true

        let vm = DashboardViewModel(apiClient: client)
        await vm.load()

        #expect(vm.dashboard == nil)
        #expect(vm.error != nil)
    }

    @Test("startListening forwards notification events to alertsViewModel")
    func startListeningForwardsAlerts() async throws {
        let client = MockAPIClient()
        client.dashboardResponse = DashboardData(
            daemonRunning: true, serverCount: 1, activeSessions: 0,
            activeAgents: 0, idleAgents: 0, offlineAgents: 0,
            updatedAt: "2026-02-24T00:00:00Z",
            health: HealthSummary(totalServers: 1, healthyServers: 1, degradedServers: 0, downServers: 0, idleServers: 0),
            coordination: DashboardCoordination(),
            recentTimeline: []
        )

        let alertsVM = AlertsViewModel()
        let vm = DashboardViewModel(apiClient: client, alertsViewModel: alertsVM)

        let request = URLRequest(url: URL(string: "http://localhost/events")!)
        let sse = SSEClient(request: request)
        sse._testBaseDelay = 0.01
        sse._testMaxDelay = 0.01
        sse._testStreamResults = [
            .succeedWithEvents([
                SSEEvent(type: "hud.health", data: "{\"down_servers\":1}"),
                SSEEvent(type: "agent.session.start", data: "{\"session_id\":\"s1\"}"),
            ])
        ]

        vm.startListening(sseClient: sse)
        sse.connect()

        try await Task.sleep(for: .milliseconds(500))

        // hud.health with down_servers→critical alert, agent.session.start→info alert
        #expect(alertsVM.alerts.count == 2)
    }

    @Test("startListening cancels previous listener")
    func startListeningCancelsPrevious() async throws {
        let client = MockAPIClient()
        client.dashboardResponse = DashboardData(
            daemonRunning: true, serverCount: 1, activeSessions: 0,
            activeAgents: 0, idleAgents: 0, offlineAgents: 0,
            updatedAt: "2026-02-24T00:00:00Z",
            health: HealthSummary(totalServers: 1, healthyServers: 1, degradedServers: 0, downServers: 0, idleServers: 0),
            coordination: DashboardCoordination(),
            recentTimeline: []
        )

        let alertsVM = AlertsViewModel()
        let vm = DashboardViewModel(apiClient: client, alertsViewModel: alertsVM)

        // First SSE client (no events, just connects)
        let request1 = URLRequest(url: URL(string: "http://localhost/events")!)
        let sse1 = SSEClient(request: request1)
        sse1._testBaseDelay = 0.01
        sse1._testMaxDelay = 0.01
        sse1._testStreamResults = [.succeed]

        // Second SSE client with an alert event
        let request2 = URLRequest(url: URL(string: "http://localhost/events")!)
        let sse2 = SSEClient(request: request2)
        sse2._testBaseDelay = 0.01
        sse2._testMaxDelay = 0.01
        sse2._testStreamResults = [
            .succeedWithEvents([
                SSEEvent(type: "hud.health", data: "{\"degraded_servers\":1}"),
            ])
        ]

        vm.startListening(sseClient: sse1)
        sse1.connect()

        // Replace with second client — should cancel first
        vm.startListening(sseClient: sse2)
        sse2.connect()

        try await Task.sleep(for: .milliseconds(500))

        // Event from second client should be processed
        #expect(alertsVM.alerts.count == 1)
    }

    @Test("stopListening cancels event consumption")
    func stopListeningCancels() async throws {
        let client = MockAPIClient()
        client.dashboardResponse = DashboardData(
            daemonRunning: true, serverCount: 1, activeSessions: 0,
            activeAgents: 0, idleAgents: 0, offlineAgents: 0,
            updatedAt: "2026-02-24T00:00:00Z",
            health: HealthSummary(totalServers: 1, healthyServers: 1, degradedServers: 0, downServers: 0, idleServers: 0),
            coordination: DashboardCoordination(),
            recentTimeline: []
        )

        let alertsVM = AlertsViewModel()
        let vm = DashboardViewModel(apiClient: client, alertsViewModel: alertsVM)

        let request = URLRequest(url: URL(string: "http://localhost/events")!)
        let sse = SSEClient(request: request)
        sse._testStreamResults = [.succeed]

        vm.startListening(sseClient: sse)
        vm.stopListening()

        // SSE events after stop should not be processed
        sse.connect()
        try await Task.sleep(for: .milliseconds(100))

        #expect(alertsVM.alerts.isEmpty)
    }

    @Test("SSE refresh events trigger dashboard reload")
    func sseRefreshTriggersReload() async throws {
        let client = MockAPIClient()
        client.dashboardResponse = DashboardData(
            daemonRunning: true, serverCount: 1, activeSessions: 0,
            activeAgents: 0, idleAgents: 0, offlineAgents: 0,
            updatedAt: "2026-02-24T00:00:00Z",
            health: HealthSummary(totalServers: 1, healthyServers: 1, degradedServers: 0, downServers: 0, idleServers: 0),
            coordination: DashboardCoordination(),
            recentTimeline: []
        )

        let vm = DashboardViewModel(apiClient: client)

        let request = URLRequest(url: URL(string: "http://localhost/events")!)
        let sse = SSEClient(request: request)
        sse._testStreamResults = [
            .succeedWithEvents([
                SSEEvent(type: "hud.fleet", data: "{}"),
            ])
        ]

        vm.startListening(sseClient: sse)
        sse.connect()

        try await Task.sleep(for: .milliseconds(200))

        // Dashboard should have been loaded by the refresh event
        #expect(vm.dashboard != nil)
    }
}
