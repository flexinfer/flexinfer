import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("AlertItem + NotificationPolicy")
struct AlertItemTests {

    // MARK: - Health event classification

    @Test("Health event with down servers → critical")
    func healthDown() {
        let event = SSEEvent(type: "hud.health", data: """
            {"down_servers": 2, "degraded_servers": 0, "healthy_servers": 1}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .critical)
        #expect(alert?.title == "Server Down")
    }

    @Test("Health event with degraded servers → warning")
    func healthDegraded() {
        let event = SSEEvent(type: "hud.health", data: """
            {"down_servers": 0, "degraded_servers": 1, "healthy_servers": 2}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .warning)
        #expect(alert?.title == "Server Degraded")
    }

    @Test("Health event all healthy → no alert")
    func healthAllGood() {
        let event = SSEEvent(type: "hud.health", data: """
            {"down_servers": 0, "degraded_servers": 0, "healthy_servers": 3}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert == nil)
    }

    // MARK: - Session events

    @Test("Session start → info alert")
    func sessionStart() {
        let event = SSEEvent(type: "agent.session.start", data: """
            {"session_id": "sess-1", "agent_id": "claude-code"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .info)
        #expect(alert?.title == "Session Started")
        #expect(alert?.relatedSessionId == "sess-1")
    }

    @Test("Session end → info alert")
    func sessionEnd() {
        let event = SSEEvent(type: "agent.session.end", data: """
            {"session_id": "sess-2"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .info)
        #expect(alert?.title == "Session Ended")
        #expect(alert?.relatedSessionId == "sess-2")
    }

    @Test("Session reaped → warning alert")
    func sessionReaped() {
        let event = SSEEvent(type: "agent.session.reaped", data: """
            {"session_id": "sess-3"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .warning)
        #expect(alert?.title == "Session Reaped")
    }

    // MARK: - Workflow events

    @Test("Workflow approved → info")
    func workflowApproved() {
        let event = SSEEvent(type: "hud.workflow.approve", data: """
            {"workflow_id": "wf-1"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.title == "Workflow Approved")
    }

    @Test("Workflow rejected → warning")
    func workflowRejected() {
        let event = SSEEvent(type: "hud.workflow.reject", data: """
            {"workflow_id": "wf-2"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .warning)
        #expect(alert?.title == "Workflow Rejected")
    }

    // MARK: - Other events

    @Test("Nudge created → info")
    func nudgeCreated() {
        let event = SSEEvent(type: "agent.nudge.created", data: """
            {"agent_id": "codex"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.title == "Agent Nudge Queued")
    }

    @Test("Handoff created → info")
    func handoffCreated() {
        let event = SSEEvent(type: "hud.handoff.created", data: """
            {"from_agent": "claude", "to_agent": "codex"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.title == "Handoff Created")
    }

    @Test("Plan complete → info")
    func planComplete() {
        let event = SSEEvent(type: "coordinator.plan.complete", data: """
            {"plan_id": "plan-1"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.title == "Plan Complete")
    }

    @Test("Unknown event type → no alert")
    func unknownEvent() {
        let event = SSEEvent(type: "some.random.event", data: "{}")
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert == nil)
    }

    // MARK: - Severity ordering

    @Test("Severity ordering: info < warning < critical")
    func severityOrdering() {
        #expect(AlertSeverity.info < AlertSeverity.warning)
        #expect(AlertSeverity.warning < AlertSeverity.critical)
        #expect(AlertSeverity.info < AlertSeverity.critical)
    }
}
