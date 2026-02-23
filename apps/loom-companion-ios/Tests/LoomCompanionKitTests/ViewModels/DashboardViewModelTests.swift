import Testing
import Foundation
@testable import LoomCompanionKit

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
}
