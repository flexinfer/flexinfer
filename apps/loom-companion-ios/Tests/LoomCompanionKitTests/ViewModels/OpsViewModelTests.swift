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
            pendingApprovals: 1,
            deprecated: true,
            deprecationMessage: "Workflow approvals are deprecated in the mobile surface; use Loom tasks and pipelines instead."
        )
        client.pipelinesResponse = MobilePipelinesResponse(
            pipelines: [
                MobilePipeline(
                    id: 77,
                    project: "services/loom-core",
                    ref: "main",
                    status: "running",
                    source: "push",
                    createdAt: "2026-02-25T10:00:00Z",
                    webURL: "https://gitlab.flexinfer.ai/services/loom-core/-/pipelines/77",
                    currentStage: "test",
                    stages: [
                        MobilePipelineStage(name: "build", status: "success", jobs: []),
                        MobilePipelineStage(name: "test", status: "running", jobs: []),
                    ],
                    completedStages: 1,
                    totalStages: 2,
                    failedJobCount: 0,
                    agentId: "codex",
                    agentType: "codex"
                )
            ],
            recentPipelines: [
                MobilePipeline(
                    id: 76,
                    project: "services/loom-core",
                    ref: "feature/recent",
                    status: "success",
                    createdAt: "2026-02-25T09:50:00Z",
                    webURL: "https://gitlab.flexinfer.ai/services/loom-core/-/pipelines/76"
                )
            ],
            summary: MobilePipelineSummary(
                running: 1,
                passed: 1,
                failed: 0,
                pending: 0,
                lastActivity: "2m ago"
            ),
            available: true
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
        client.controlPlaneResponse = MobileControlPlaneResponse(
            cost: MobileControlPlaneCost(
                enabled: true,
                timestamp: "2026-02-25T10:00:00Z",
                totalCalls: 10,
                totalErrors: 1,
                totalDenied: 0,
                totalCached: 2,
                totalDurationMs: 1200,
                topAgent: nil,
                topServer: nil
            ),
            rbac: MobileControlPlaneRBAC(
                enabled: true,
                defaultPolicy: "allow",
                roleCount: 2,
                bindingCount: 3,
                globalDenyCount: 0,
                rateLimitCount: 1,
                deniedCount: 0
            ),
            otel: MobileControlPlaneOTel(
                otlpConfigured: true,
                otlpEndpoint: "http://otel-collector:4317",
                jsonLogsEnabled: true,
                tracedServers: 4,
                totalServers: 4,
                traceCoverage: "100%"
            ),
            health: MobileControlPlaneHealth(
                totalServers: 4,
                healthyServers: 4,
                degradedServers: 0,
                downServers: 0,
                idleServers: 0,
                hubTargets: 4,
                localTargets: 0,
                unavailableTargets: 0
            )
        )

        client.sandboxResponse = MobileSandboxSummary(
            available: true,
            projects: [
                MobileSandboxProject(project: "loom-core", status: "running", agentId: "claude-code", uptime: "5m", backend: "k8s"),
            ],
            totalRunning: 1,
            backend: "k8s"
        )

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.error == nil)
        #expect(vm.warningMessage == nil)
        #expect(vm.tasks.count == 1)
        #expect(vm.workflows.count == 1)
        #expect(vm.pendingApprovals == 1)
        #expect(vm.workflowsDeprecated == true)
        #expect(vm.workflowsDeprecationMessage?.contains("deprecated") == true)
        #expect(vm.pipelines.count == 1)
        #expect(vm.recentPipelines.count == 1)
        #expect(vm.pipelineSummary?.running == 1)
        #expect(vm.pipelinesAvailable == true)
        #expect(vm.memoryStats?.totalItems == 60)
        #expect(vm.graphStats?.totalEntities == 12)
        #expect(vm.topology != nil)
        #expect(vm.sandboxSummary?.available == true)
        #expect(vm.sandboxSummary?.totalRunning == 1)
        #expect(vm.sandboxSummary?.projects.count == 1)
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
        client.pipelinesResponse = MobilePipelinesResponse(pipelines: [], available: true)
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
        client.streamResponse = MobileStreamResponse(entries: [])
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
        client.controlPlaneResponse = MobileControlPlaneResponse(
            cost: MobileControlPlaneCost(
                enabled: true,
                timestamp: nil,
                totalCalls: 0,
                totalErrors: 0,
                totalDenied: 0,
                totalCached: 0,
                totalDurationMs: 0,
                topAgent: nil,
                topServer: nil
            ),
            rbac: MobileControlPlaneRBAC(
                enabled: false,
                defaultPolicy: nil,
                roleCount: 0,
                bindingCount: 0,
                globalDenyCount: 0,
                rateLimitCount: 0,
                deniedCount: 0
            ),
            otel: MobileControlPlaneOTel(
                otlpConfigured: false,
                otlpEndpoint: nil,
                jsonLogsEnabled: false,
                tracedServers: 0,
                totalServers: 0,
                traceCoverage: nil
            ),
            health: MobileControlPlaneHealth(
                totalServers: 0,
                healthyServers: 0,
                degradedServers: 0,
                downServers: 0,
                idleServers: 0,
                hubTargets: 0,
                localTargets: 0,
                unavailableTargets: 0
            )
        )
        client.sandboxResponse = MobileSandboxSummary(available: false, projects: [], totalRunning: 0, backend: "unknown")
        client.endpointFailures["/api/mobile/v1/graph/path"] = .apiError(code: .upstreamError, message: "path unavailable", requestId: "req-path-1")

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.error == nil)
        #expect(vm.warningMessage != nil)
        #expect(vm.graphPath == nil)
        #expect(vm.graphEntities.count == 2)
        #expect(vm.sandboxSummary?.available == false)
    }

    @Test("Workflow failures no longer count as core warnings")
    func workflowFailureDoesNotWarn() async {
        let client = MockAPIClient()
        client.tasksResponse = MobileTasksResponse(tasks: [], counts: MobileTaskCounts(pending: 0, inProgress: 0, blocked: 0, completed: 0))
        client.pipelinesResponse = MobilePipelinesResponse(pipelines: [], available: true)
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
            stats: MobileGraphStats(totalEntities: 0, totalRelations: 0, entityTypes: [:], relationTypes: [:])
        )
        client.graphEntitiesResponse = MobileGraphEntitiesResponse(entities: [])
        client.reasoningChainsResponse = MobileReasoningChainsResponse(chains: [])
        client.controlPlaneResponse = MobileControlPlaneResponse(
            cost: MobileControlPlaneCost(
                enabled: false,
                timestamp: nil,
                totalCalls: 0,
                totalErrors: 0,
                totalDenied: 0,
                totalCached: 0,
                totalDurationMs: 0,
                topAgent: nil,
                topServer: nil
            ),
            rbac: MobileControlPlaneRBAC(
                enabled: false,
                defaultPolicy: nil,
                roleCount: 0,
                bindingCount: 0,
                globalDenyCount: 0,
                rateLimitCount: 0,
                deniedCount: 0
            ),
            otel: MobileControlPlaneOTel(
                otlpConfigured: false,
                otlpEndpoint: nil,
                jsonLogsEnabled: false,
                tracedServers: 0,
                totalServers: 0,
                traceCoverage: nil
            ),
            health: MobileControlPlaneHealth(
                totalServers: 0,
                healthyServers: 0,
                degradedServers: 0,
                downServers: 0,
                idleServers: 0,
                hubTargets: 0,
                localTargets: 0,
                unavailableTargets: 0
            )
        )
        client.sandboxResponse = MobileSandboxSummary(available: false, projects: [], totalRunning: 0, backend: "unknown")
        client.endpointFailures["/api/mobile/v1/workflows"] = .apiError(code: .upstreamError, message: "workflow service unavailable", requestId: "req-workflows-1")

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        #expect(vm.error == nil)
        #expect(vm.warningMessage == nil)
        #expect(vm.workflows.isEmpty)
        #expect(vm.pipelinesAvailable == true)
    }

    @Test("Task and pipeline failures preserve last successful data")
    func taskAndPipelineFailuresPreserveLastKnownData() async {
        let client = MockAPIClient()
        client.tasksResponse = MobileTasksResponse(
            tasks: [
                MobileTask(
                    id: "task-1",
                    sessionId: "sess-1",
                    agentId: "codex",
                    namespace: "loom-core/main",
                    title: "Preserve me",
                    context: nil,
                    priority: "high",
                    status: .inProgress,
                    tags: [],
                    blockedBy: [],
                    createdAt: "2026-02-25T10:00:00Z",
                    updatedAt: "2026-02-25T10:10:00Z"
                ),
            ],
            counts: MobileTaskCounts(pending: 0, inProgress: 1, blocked: 0, completed: 0)
        )
        client.workflowsResponse = MobileWorkflowsResponse(workflows: [], pendingApprovals: 0)
        client.pipelinesResponse = MobilePipelinesResponse(
            pipelines: [
                MobilePipeline(
                    id: 77,
                    project: "services/loom-core",
                    ref: "main",
                    status: "running",
                    source: "push",
                    createdAt: "2026-02-25T10:00:00Z",
                    webURL: nil,
                    currentStage: "test",
                    stages: [],
                    completedStages: 1,
                    totalStages: 2,
                    failedJobCount: 0,
                    agentId: "codex",
                    agentType: "codex"
                )
            ],
            available: true
        )
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
            stats: MobileGraphStats(totalEntities: 0, totalRelations: 0, entityTypes: [:], relationTypes: [:])
        )
        client.graphEntitiesResponse = MobileGraphEntitiesResponse(entities: [])
        client.reasoningChainsResponse = MobileReasoningChainsResponse(chains: [])
        client.controlPlaneResponse = MobileControlPlaneResponse(
            cost: MobileControlPlaneCost(
                enabled: false,
                timestamp: nil,
                totalCalls: 0,
                totalErrors: 0,
                totalDenied: 0,
                totalCached: 0,
                totalDurationMs: 0,
                topAgent: nil,
                topServer: nil
            ),
            rbac: MobileControlPlaneRBAC(
                enabled: false,
                defaultPolicy: nil,
                roleCount: 0,
                bindingCount: 0,
                globalDenyCount: 0,
                rateLimitCount: 0,
                deniedCount: 0
            ),
            otel: MobileControlPlaneOTel(
                otlpConfigured: false,
                otlpEndpoint: nil,
                jsonLogsEnabled: false,
                tracedServers: 0,
                totalServers: 0,
                traceCoverage: nil
            ),
            health: MobileControlPlaneHealth(
                totalServers: 0,
                healthyServers: 0,
                degradedServers: 0,
                downServers: 0,
                idleServers: 0,
                hubTargets: 0,
                localTargets: 0,
                unavailableTargets: 0
            )
        )
        client.sandboxResponse = MobileSandboxSummary(available: false, projects: [], totalRunning: 0, backend: "unknown")

        let vm = OpsViewModel(apiClient: client)
        await vm.load()

        client.endpointFailures["/api/mobile/v1/tasks"] = .apiError(code: .upstreamError, message: "tasks unavailable", requestId: "req-tasks-1")
        client.endpointFailures["/api/mobile/v1/pipelines"] = .apiError(code: .upstreamError, message: "pipelines unavailable", requestId: "req-pipes-1")

        await vm.load()

        #expect(vm.tasks.count == 1)
        #expect(vm.tasks.first?.title == "Preserve me")
        #expect(vm.taskCounts.inProgress == 1)
        #expect(vm.pipelines.count == 1)
        #expect(vm.pipelines.first?.currentStage == "test")
        #expect(vm.pipelinesAvailable == true)
        #expect(vm.warningMessage?.contains("tasks") == true)
        #expect(vm.warningMessage?.contains("pipelines") == true)
    }

    @Test("Create session mutation success returns status message")
    func createSessionMutationSuccess() async {
        let client = MockAPIClient()
        client.createSessionResponse = SessionCreateResponse(sessionId: "sess-new", recalledContext: nil, alreadyExisted: false)
        let vm = OpsViewModel(apiClient: client)

        await vm.createSession(agentID: "codex-gpt5", namespace: "loom-core/mobile", description: "test", autoRecall: true)

        #expect(vm.mutationErrorMessage == nil)
        #expect(vm.mutationStatusMessage == "Session started: sess-new")
        #expect(vm.isMutatingSession == false)
    }

    @Test("End session mutation success returns status message")
    func endSessionMutationSuccess() async {
        let client = MockAPIClient()
        client.endSessionResponse = SessionEndResponse(ended: true, sessionId: "sess-1")
        let vm = OpsViewModel(apiClient: client)

        await vm.endSession(sessionID: "sess-1", summarize: false)

        #expect(vm.mutationErrorMessage == nil)
        #expect(vm.mutationStatusMessage == "Session ended: sess-1")
        #expect(vm.isMutatingSession == false)
    }

    @Test("Create session mutation requires agent ID")
    func createSessionMutationValidation() async {
        let client = MockAPIClient()
        let vm = OpsViewModel(apiClient: client)

        await vm.createSession(agentID: "   ", namespace: nil, description: nil, autoRecall: false)

        #expect(vm.mutationErrorMessage == "Agent ID is required")
    }

    @Test("End session mutation requires session ID")
    func endSessionMutationValidation() async {
        let client = MockAPIClient()
        let vm = OpsViewModel(apiClient: client)

        await vm.endSession(sessionID: "  ", summarize: true)

        #expect(vm.mutationErrorMessage == "Session ID is required")
    }

    @Test("Start sandbox mutation refreshes sandbox summary")
    func startSandboxMutationSuccess() async {
        let client = MockAPIClient()
        client.sandboxStartResponse = MobileSandboxStartResponse(started: true, project: "loom-core")
        client.sandboxResponse = MobileSandboxSummary(
            available: true,
            projects: [
                MobileSandboxProject(project: "loom-core", status: "running", agentId: "codex", uptime: "10s", backend: "k8s"),
            ],
            totalRunning: 1,
            backend: "k8s"
        )
        let vm = OpsViewModel(apiClient: client)

        await vm.startSandbox(project: "loom-core", agentID: "codex")

        #expect(vm.sandboxMutationError == nil)
        #expect(vm.sandboxMutationMessage == "Sandbox started: loom-core")
        #expect(vm.sandboxSummary?.totalRunning == 1)
        #expect(vm.sandboxSummary?.projects.first?.project == "loom-core")
        #expect(vm.isMutatingSandbox == false)
    }

    @Test("Stop sandbox mutation refreshes sandbox summary")
    func stopSandboxMutationSuccess() async {
        let client = MockAPIClient()
        client.sandboxStopResponse = MobileSandboxStopResponse(stopped: true, project: "loom-core")
        client.sandboxResponse = MobileSandboxSummary(
            available: true,
            projects: [],
            totalRunning: 0,
            backend: "k8s"
        )
        let vm = OpsViewModel(apiClient: client)

        await vm.stopSandbox(project: "loom-core")

        #expect(vm.sandboxMutationError == nil)
        #expect(vm.sandboxMutationMessage == "Sandbox stopped: loom-core")
        #expect(vm.sandboxSummary?.totalRunning == 0)
        #expect(vm.sandboxSummary?.projects.isEmpty == true)
        #expect(vm.isMutatingSandbox == false)
    }

    @Test("Sandbox mutations require project name")
    func sandboxMutationValidation() async {
        let client = MockAPIClient()
        let vm = OpsViewModel(apiClient: client)

        await vm.startSandbox(project: "   ", agentID: "codex")
        #expect(vm.sandboxMutationError == "Project is required")

        await vm.stopSandbox(project: " ")
        #expect(vm.sandboxMutationError == "Project is required")
    }
}
