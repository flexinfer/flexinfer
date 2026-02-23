import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("SessionsViewModel")
struct SessionsViewModelTests {

    @Test("Filter by status")
    func filterByStatus() async {
        let client = MockAPIClient()
        client.sessionsResponse = SessionsResponse(sessions: [
            makeSession(id: "s1", status: .active),
            makeSession(id: "s2", status: .ended),
            makeSession(id: "s3", status: .active),
        ])

        let vm = SessionsViewModel(apiClient: client)
        await vm.load()

        vm.statusFilter = .active
        #expect(vm.filteredSessions.count == 2)

        vm.statusFilter = .ended
        #expect(vm.filteredSessions.count == 1)

        vm.statusFilter = nil
        #expect(vm.filteredSessions.count == 3)
    }

    @Test("Filter by agent")
    func filterByAgent() async {
        let client = MockAPIClient()
        client.sessionsResponse = SessionsResponse(sessions: [
            makeSession(id: "s1", agentId: "claude-code"),
            makeSession(id: "s2", agentId: "codex"),
            makeSession(id: "s3", agentId: "claude-code"),
        ])

        let vm = SessionsViewModel(apiClient: client)
        await vm.load()

        vm.agentFilter = "claude-code"
        #expect(vm.filteredSessions.count == 2)

        vm.agentFilter = "codex"
        #expect(vm.filteredSessions.count == 1)
    }

    @Test("Search text filtering")
    func searchText() async {
        let client = MockAPIClient()
        client.sessionsResponse = SessionsResponse(sessions: [
            makeSession(id: "s1", namespace: "loom-core/main", description: "Mobile API work"),
            makeSession(id: "s2", namespace: "loom-core/feature", description: "Bug fix"),
        ])

        let vm = SessionsViewModel(apiClient: client)
        await vm.load()

        vm.searchText = "mobile"
        #expect(vm.filteredSessions.count == 1)
        #expect(vm.filteredSessions[0].id == "s1")

        vm.searchText = "loom-core"
        #expect(vm.filteredSessions.count == 2)
    }

    @Test("Available agents list")
    func availableAgents() async {
        let client = MockAPIClient()
        client.sessionsResponse = SessionsResponse(sessions: [
            makeSession(id: "s1", agentId: "claude-code"),
            makeSession(id: "s2", agentId: "codex"),
            makeSession(id: "s3", agentId: "claude-code"),
        ])

        let vm = SessionsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.availableAgents.count == 2)
        #expect(vm.availableAgents.contains("claude-code"))
        #expect(vm.availableAgents.contains("codex"))
    }

    @Test("Create session success reloads list")
    func createSessionSuccess() async {
        let client = MockAPIClient()
        client.sessionsResponse = SessionsResponse(sessions: [
            makeSession(id: "s1", status: .active),
        ])
        client.createSessionResponse = SessionCreateResponse(sessionId: "s2")

        let vm = SessionsViewModel(apiClient: client)
        await vm.load()
        #expect(vm.sessions.count == 1)

        await vm.createSession(agentId: "claude-code", namespace: "test/ns")

        #expect(vm.createError == nil)
        #expect(vm.isCreating == false)
        // After create, load() is called again — sessions re-fetched from mock
        #expect(vm.sessions.count == 1)
    }

    @Test("Create session error sets createError")
    func createSessionError() async {
        let client = MockAPIClient()
        client.sessionsResponse = SessionsResponse(sessions: [])
        client.shouldFail = true
        client.failError = .apiError(code: .rateLimited, message: "too many requests", requestId: "r1")

        let vm = SessionsViewModel(apiClient: client)

        await vm.createSession(agentId: "claude-code")

        #expect(vm.createError != nil)
        #expect(vm.isCreating == false)
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
