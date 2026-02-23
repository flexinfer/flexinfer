import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("SessionDetailViewModel")
struct SessionDetailViewModelTests {

    @Test("End session success")
    func endSessionSuccess() async {
        let client = MockAPIClient()
        client.sessionDetailResponse = SessionDetailResponse(session: makeSession(id: "s1", status: .active))
        client.sessionEventsResponse = SessionEventsResponse(sessionId: "s1", events: [])
        client.endSessionResponse = SessionEndResponse(ended: true, sessionId: "s1")

        let vm = SessionDetailViewModel(apiClient: client)
        await vm.load(sessionId: "s1")
        #expect(vm.session?.status == .active)

        await vm.endSession(summarize: false)

        #expect(vm.sessionEnded == true)
        #expect(vm.session?.status == .ended)
        #expect(vm.endError == nil)
    }

    @Test("End session error")
    func endSessionError() async {
        let client = MockAPIClient()
        client.sessionDetailResponse = SessionDetailResponse(session: makeSession(id: "s1", status: .active))
        client.sessionEventsResponse = SessionEventsResponse(sessionId: "s1", events: [])

        let vm = SessionDetailViewModel(apiClient: client)
        await vm.load(sessionId: "s1")

        // Make the next call fail
        client.shouldFail = true
        client.failError = .apiError(code: .forbidden, message: "no permission", requestId: "r1")

        await vm.endSession(summarize: true)

        #expect(vm.sessionEnded == false)
        #expect(vm.endError != nil)
        #expect(vm.session?.status == .active)
    }

    @Test("Load session detail and events")
    func loadDetail() async {
        let client = MockAPIClient()
        client.sessionDetailResponse = SessionDetailResponse(session: makeSession(id: "s1"))
        client.sessionEventsResponse = SessionEventsResponse(sessionId: "s1", events: [
            TimelineEntry(timestamp: "2026-02-23T10:00:00Z", eventType: "agent.session.start", agentId: "claude-code", agentType: "claude-code", data: nil),
        ])

        let vm = SessionDetailViewModel(apiClient: client)
        await vm.load(sessionId: "s1")

        #expect(vm.session?.id == "s1")
        #expect(vm.events.count == 1)
        #expect(vm.error == nil)
    }
}

private func makeSession(
    id: String,
    agentId: String = "claude-code",
    namespace: String = "test/ns",
    status: SessionStatus = .active,
    description: String = ""
) -> SessionInfo {
    SessionInfo(
        id: id, agentId: agentId, namespace: namespace,
        status: status, description: description,
        startedAt: "2026-02-23T10:00:00Z", endedAt: nil,
        entryCount: 10, totalTokens: 1000
    )
}
