import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("AlertItem + NotificationPolicy")
struct AlertItemTests {

    // MARK: - Health event classification

    @Test("Health event with down servers → critical, timeSensitive")
    func healthDown() {
        let event = SSEEvent(type: "hud.health", data: """
            {"down_servers": 2, "degraded_servers": 0, "healthy_servers": 1}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .critical)
        #expect(alert?.interruptionLevel == .timeSensitive)
        #expect(alert?.title == "Server Down")
        #expect(alert?.allowedActions == [.viewDashboard, .acknowledge])
        #expect(alert?.primaryAction == .viewDashboard)
    }

    @Test("Health event with degraded servers → warning, active")
    func healthDegraded() {
        let event = SSEEvent(type: "hud.health", data: """
            {"down_servers": 0, "degraded_servers": 1, "healthy_servers": 2}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .warning)
        #expect(alert?.interruptionLevel == .active)
        #expect(alert?.title == "Server Degraded")
        #expect(alert?.allowedActions == [.viewDashboard, .acknowledge])
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

    @Test("Session start → info, passive, viewSession action")
    func sessionStart() {
        let event = SSEEvent(type: "agent.session.start", data: """
            {"session_id": "sess-1", "agent_id": "claude-code"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .info)
        #expect(alert?.interruptionLevel == .passive)
        #expect(alert?.title == "Session Started")
        #expect(alert?.relatedSessionId == "sess-1")
        #expect(alert?.allowedActions == [.viewSession, .acknowledge])
        #expect(alert?.primaryAction == .viewSession)
    }

    @Test("Session end → info, passive, viewSession action")
    func sessionEnd() {
        let event = SSEEvent(type: "agent.session.end", data: """
            {"session_id": "sess-2"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .info)
        #expect(alert?.interruptionLevel == .passive)
        #expect(alert?.title == "Session Ended")
        #expect(alert?.relatedSessionId == "sess-2")
        #expect(alert?.primaryAction == .viewSession)
    }

    @Test("Session reaped → warning, active, viewSession action")
    func sessionReaped() {
        let event = SSEEvent(type: "agent.session.reaped", data: """
            {"session_id": "sess-3"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert != nil)
        #expect(alert?.severity == .warning)
        #expect(alert?.interruptionLevel == .active)
        #expect(alert?.title == "Session Reaped")
        #expect(alert?.allowedActions == [.viewSession, .acknowledge])
    }

    // MARK: - Workflow events

    @Test("Workflow approved → info, passive, viewWorkflow action")
    func workflowApproved() {
        let event = SSEEvent(type: "hud.workflow.approve", data: """
            {"workflow_id": "wf-1"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.interruptionLevel == .passive)
        #expect(alert?.title == "Workflow Approved")
        #expect(alert?.relatedWorkflowId == "wf-1")
        #expect(alert?.allowedActions == [.viewWorkflow, .acknowledge])
        #expect(alert?.primaryAction == .viewWorkflow)
    }

    @Test("Workflow rejected → warning, active, viewWorkflow action")
    func workflowRejected() {
        let event = SSEEvent(type: "hud.workflow.reject", data: """
            {"workflow_id": "wf-2"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .warning)
        #expect(alert?.interruptionLevel == .active)
        #expect(alert?.title == "Workflow Rejected")
        #expect(alert?.relatedWorkflowId == "wf-2")
        #expect(alert?.allowedActions == [.viewWorkflow, .acknowledge])
        #expect(alert?.primaryAction == .viewWorkflow)
    }

    // MARK: - Other events

    @Test("Nudge created → info, passive, acknowledge only")
    func nudgeCreated() {
        let event = SSEEvent(type: "agent.nudge.created", data: """
            {"agent_id": "codex"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.interruptionLevel == .passive)
        #expect(alert?.title == "Agent Nudge Queued")
        #expect(alert?.primaryAction == .acknowledge)
    }

    @Test("Handoff created → info, passive")
    func handoffCreated() {
        let event = SSEEvent(type: "hud.handoff.created", data: """
            {"from_agent": "claude", "to_agent": "codex"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.interruptionLevel == .passive)
        #expect(alert?.title == "Handoff Created")
    }

    @Test("Plan complete → info, passive")
    func planComplete() {
        let event = SSEEvent(type: "coordinator.plan.complete", data: """
            {"plan_id": "plan-1"}
            """)
        let alert = NotificationPolicy.classify(event: event)
        #expect(alert?.severity == .info)
        #expect(alert?.interruptionLevel == .passive)
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

    // MARK: - Interruption level ordering

    @Test("InterruptionLevel ordering: passive < active < timeSensitive < critical")
    func interruptionLevelOrdering() {
        #expect(InterruptionLevel.passive < InterruptionLevel.active)
        #expect(InterruptionLevel.active < InterruptionLevel.timeSensitive)
        #expect(InterruptionLevel.timeSensitive < InterruptionLevel.critical)
    }

    // MARK: - Action constraints

    @Test("Info events never use active or timeSensitive interruption")
    func infoEventsAreQuiet() {
        let infoEvents = [
            "agent.session.start",
            "agent.session.end",
            "agent.nudge.created",
            "hud.workflow.approve",
            "hud.handoff.created",
            "coordinator.plan.complete",
        ]
        for eventType in infoEvents {
            let event = SSEEvent(type: eventType, data: """
                {"session_id": "s1", "agent_id": "a1", "workflow_id": "w1", "from_agent": "a", "to_agent": "b", "plan_id": "p1"}
                """)
            let alert = NotificationPolicy.classify(event: event)
            if let alert {
                #expect(alert.interruptionLevel == .passive, "Event \(eventType) should be passive, got \(alert.interruptionLevel)")
            }
        }
    }

    @Test("No alert contains mutation actions")
    func noMutationActions() {
        let allEvents: [(String, String)] = [
            ("hud.health", "{\"down_servers\": 1}"),
            ("hud.health", "{\"degraded_servers\": 1}"),
            ("agent.session.reaped", "{\"session_id\": \"s1\"}"),
            ("agent.session.start", "{\"session_id\": \"s1\", \"agent_id\": \"a1\"}"),
            ("agent.session.end", "{\"session_id\": \"s1\"}"),
            ("agent.nudge.created", "{\"agent_id\": \"a1\"}"),
            ("hud.workflow.approve", "{\"workflow_id\": \"w1\"}"),
            ("hud.workflow.reject", "{\"workflow_id\": \"w1\"}"),
            ("hud.handoff.created", "{\"from_agent\": \"a\", \"to_agent\": \"b\"}"),
            ("coordinator.plan.complete", "{\"plan_id\": \"p1\"}"),
        ]
        for (eventType, data) in allEvents {
            let event = SSEEvent(type: eventType, data: data)
            if let alert = NotificationPolicy.classify(event: event) {
                for action in alert.allowedActions {
                    #expect(
                        action == .viewSession || action == .viewWorkflow || action == .viewDashboard || action == .acknowledge,
                        "Event \(eventType) has unexpected action \(action)"
                    )
                }
            }
        }
    }

    @Test("Primary action returns first non-acknowledge action")
    func primaryActionLogic() {
        let alert1 = AlertItem(severity: .info, title: "T", message: "M", eventType: "e", allowedActions: [.viewSession, .acknowledge])
        #expect(alert1.primaryAction == .viewSession)

        let alert2 = AlertItem(severity: .info, title: "T", message: "M", eventType: "e", allowedActions: [.acknowledge])
        #expect(alert2.primaryAction == .acknowledge)

        let alert3 = AlertItem(severity: .info, title: "T", message: "M", eventType: "e", allowedActions: [.viewDashboard, .acknowledge])
        #expect(alert3.primaryAction == .viewDashboard)
    }
}
