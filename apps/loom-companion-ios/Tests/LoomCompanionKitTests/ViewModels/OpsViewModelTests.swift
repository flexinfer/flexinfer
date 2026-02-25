import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("OpsViewModel")
struct OpsViewModelTests {

    @Test("Load success populates parity datasets")
    func loadSuccess() async {
        let client = MockAPIClient()
        client.tasksResponse = MobileTasksResponse(
            tasks: [
                MobileTask(
                    id: "task-1",
                    sessionId: "sess-1",
                    agentId: "codex",
                    namespace: "loom-core/main",
                    title: "Investigate mobile parity",
                    context: "Read-only scope",
                    priority: "high",
                    status: .inProgress,
                    tags: ["mobile"],
                    blockedBy: [],
                    createdAt: "2026-02-25T10:00:00Z",
                    updatedAt: "2026-02-25T10:10:00Z"
                ),
            ],
            counts: MobileTaskCounts(pending: 0, inProgress: 1, blocked: 0, completed: 0)
        )
        client.workflowsResponse = MobileWorkflowsResponse(
            workflows: [
                MobileWorkflow(
                    id: "wf-1",
                    name: "Summarize Session",
                    status: .running,
                    currentStep: "analyze",
                    progress: 0.4,
                    startedAt: "2026-02-25T10:00:00Z",
                    completedAt: nil,
                    error: nil
                ),
            ],
            pendingApprovals: 1
        )
        client.presenceResponse = MobilePresenceResponse(
            agents: [],
            claims: [],
            worktrees: [],
            summary: MobilePresenceSummary(
                activeAgents: 2,
                idleAgents: 1,
                offlineAgents: 0,
                totalAgents: 3,
                claimCount: 0,
                worktreeCount: 0
            )
        )
        client.memoryStatsResponse = MobileMemoryStatsResponse(
            stats: MobileMemoryStats(
                workingMemory: MobileMemoryTierStats(items: 10, tokens: 1000),
                shortTermMemory: MobileMemoryTierStats(items: 20, tokens: 2000),
                longTermMemory: MobileMemoryTierStats(items: 30, tokens: 3000),
                totalItems: 60,
                totalTokens: 6000,
                compression: MobileMemoryCompression(
                    ratio: 0.5,
                    added24h: 4,
                    compressed24h: 2,
                    estimatedSaved: 3000,
                    compressedItems: 2
                )
            )
        )
        client.memoryItemsResponse = MobileMemoryItemsResponse(items: [], tier: .working)
        client.streamResponse = MobileStreamResponse(entries: [])
        client.topologyResponse = MobileTopologyResponse(nodes: [], edges: [], clusters: [], updatedAt: "2026-02-25T10:00:00Z")
        client.graphStatsResponse = MobileGraphStatsResponse(
            stats: MobileGraphStats(totalEntities: 12, totalRelations: 34, entityTypes: [:], relationTypes: [:])
        )
        client.graphEntitiesResponse = MobileGraphEntitiesResponse(entities: [])
        client.graphPathResponse = MobileGraphPathResponse(path: MobileGraphPath(nodes: [], length: 0))
        client.reasoningChainsResponse = MobileReasoningChainsResponse(chains: [])

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.error == nil)
        #expect(vm.warningMessage == nil)
        #expect(vm.tasks.count == 1)
        #expect(vm.workflows.count == 1)
        #expect(vm.pendingApprovals == 1)
        #expect(vm.memoryStats?.totalItems == 60)
        #expect(vm.graphStats?.totalEntities == 12)
        #expect(vm.topology != nil)
    }

    @Test("Load failure surfaces API error")
    func loadFailure() async {
        let client = MockAPIClient()
        client.shouldFail = true
        client.failError = .apiError(code: .upstreamError, message: "service unavailable", requestId: "req-ops-1")

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.error != nil)
        #expect(vm.isLoading == false)
        #expect(vm.tasks.isEmpty)
    }

    @Test("Partial failure does not hard-fail Ops load")
    func partialFailureWarning() async {
        let client = MockAPIClient()
        client.tasksResponse = MobileTasksResponse(tasks: [], counts: MobileTaskCounts(pending: 0, inProgress: 0, blocked: 0, completed: 0))
        client.workflowsResponse = MobileWorkflowsResponse(workflows: [], pendingApprovals: 0)
        client.presenceResponse = MobilePresenceResponse(
            agents: [],
            claims: [],
            worktrees: [],
            summary: MobilePresenceSummary(
                activeAgents: 0,
                idleAgents: 0,
                offlineAgents: 0,
                totalAgents: 0,
                claimCount: 0,
                worktreeCount: 0
            )
        )
        client.memoryStatsResponse = MobileMemoryStatsResponse(
            stats: MobileMemoryStats(
                workingMemory: MobileMemoryTierStats(items: 0, tokens: 0),
                shortTermMemory: MobileMemoryTierStats(items: 0, tokens: 0),
                longTermMemory: MobileMemoryTierStats(items: 0, tokens: 0),
                totalItems: 0,
                totalTokens: 0,
                compression: MobileMemoryCompression(ratio: 0, added24h: 0, compressed24h: 0, estimatedSaved: 0, compressedItems: 0)
            )
        )
        client.memoryItemsResponse = MobileMemoryItemsResponse(items: [], tier: .working)
        client.streamResponse = MobileStreamResponse(entries: [])
        client.topologyResponse = MobileTopologyResponse(nodes: [], edges: [], clusters: [], updatedAt: "2026-02-25T10:00:00Z")
        client.graphStatsResponse = MobileGraphStatsResponse(
            stats: MobileGraphStats(totalEntities: 2, totalRelations: 1, entityTypes: [:], relationTypes: [:])
        )
        client.graphEntitiesResponse = MobileGraphEntitiesResponse(
            entities: [
                MobileGraphEntity(id: "ent-1", name: "Entity One", entityType: "note", description: nil, namespace: nil, properties: [:]),
                MobileGraphEntity(id: "ent-2", name: "Entity Two", entityType: "note", description: nil, namespace: nil, properties: [:]),
            ]
        )
        client.reasoningChainsResponse = MobileReasoningChainsResponse(chains: [])
        client.endpointFailures["/api/mobile/v1/graph/path"] = .apiError(code: .upstreamError, message: "path unavailable", requestId: "req-path-1")

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.error == nil)
        #expect(vm.warningMessage != nil)
        #expect(vm.graphPath == nil)
        #expect(vm.graphEntities.count == 2)
    }
}
