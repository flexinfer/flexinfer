import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("AlertsViewModel")
struct AlertsViewModelTests {

    @Test("SSE event creates alert")
    func sseEventCreatesAlert() {
        let vm = AlertsViewModel()
        let event = SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "claude"}
            """)
        vm.handleSSEEvent(event)

        #expect(vm.alerts.count == 1)
        #expect(vm.alerts[0].severity == .info)
        #expect(vm.alerts[0].title == "Session Started")
        #expect(vm.alerts[0].relatedSessionId == "s1")
    }

    @Test("Non-alert event is ignored")
    func nonAlertEventIgnored() {
        let vm = AlertsViewModel()
        let event = SSEEvent(type: "hud.fleet", data: "{}")
        vm.handleSSEEvent(event)

        #expect(vm.alerts.isEmpty)
    }

    @Test("Newest alert is first in list")
    func newestFirst() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "a1"}
            """))
        vm.handleSSEEvent(SSEEvent(type: "agent.session.end", data: """
            {"session_id": "s2"}
            """))

        #expect(vm.alerts.count == 2)
        #expect(vm.alerts[0].title == "Session Ended")
        #expect(vm.alerts[1].title == "Session Started")
    }

    @Test("Max alert limit evicts oldest")
    func maxAlertLimit() {
        let vm = AlertsViewModel()
        for i in 0..<120 {
            vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
                {"session_id": "s\(i)", "agent_id": "a"}
                """))
        }

        #expect(vm.alerts.count == AlertsViewModel.maxAlerts)
        // Most recent should be first
        #expect(vm.alerts[0].relatedSessionId == "s119")
    }

    @Test("Unread count")
    func unreadCount() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "a"}
            """))
        vm.handleSSEEvent(SSEEvent(type: "agent.session.end", data: """
            {"session_id": "s2"}
            """))

        #expect(vm.unreadCount == 2)
    }

    @Test("Mark single alert read")
    func markRead() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "a"}
            """))
        vm.handleSSEEvent(SSEEvent(type: "agent.session.end", data: """
            {"session_id": "s2"}
            """))

        let firstId = vm.alerts[0].id
        vm.markRead(firstId)

        #expect(vm.unreadCount == 1)
        #expect(vm.alerts[0].isRead == true)
        #expect(vm.alerts[1].isRead == false)
    }

    @Test("Mark all read")
    func markAllRead() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "a"}
            """))
        vm.handleSSEEvent(SSEEvent(type: "agent.session.end", data: """
            {"session_id": "s2"}
            """))

        vm.markAllRead()
        #expect(vm.unreadCount == 0)
    }

    @Test("Clear all removes everything")
    func clearAll() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "a"}
            """))
        vm.clearAll()

        #expect(vm.alerts.isEmpty)
        #expect(vm.unreadCount == 0)
    }

    @Test("Critical alerts filter")
    func criticalAlerts() {
        let vm = AlertsViewModel()

        // Info-level alert
        vm.handleSSEEvent(SSEEvent(type: "agent.session.start", data: """
            {"session_id": "s1", "agent_id": "a"}
            """))

        // Critical alert
        vm.handleSSEEvent(SSEEvent(type: "hud.health", data: """
            {"down_servers": 1, "degraded_servers": 0, "healthy_servers": 2}
            """))

        #expect(vm.criticalAlerts.count == 1)
        #expect(vm.criticalAlerts[0].severity == .critical)
    }

    @Test("Critical alerts exclude read items")
    func criticalAlertsExcludeRead() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "hud.health", data: """
            {"down_servers": 1, "degraded_servers": 0}
            """))

        #expect(vm.criticalAlerts.count == 1)

        vm.markRead(vm.alerts[0].id)
        #expect(vm.criticalAlerts.isEmpty)
    }

    @Test("Health event all healthy produces no alert")
    func healthyNoAlert() {
        let vm = AlertsViewModel()
        vm.handleSSEEvent(SSEEvent(type: "hud.health", data: """
            {"down_servers": 0, "degraded_servers": 0, "healthy_servers": 3}
            """))
        #expect(vm.alerts.isEmpty)
    }
}
