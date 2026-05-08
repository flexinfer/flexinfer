import Foundation
@testable import LoomCompanionKit

/// Mock API client for ViewModel tests.
final class MockAPIClient: LoomAPIClientProtocol, @unchecked Sendable {
    var shouldFail = false
    var failError: LoomAPIError = .apiError(code: .unauthorized, message: "mock error", requestId: "mock")
    var endpointFailures: [String: LoomAPIError] = [:]

    var dashboardResponse: DashboardData?
    var sessionsResponse: SessionsResponse?
    var sessionDetailResponse: SessionDetailResponse?
    var sessionEventsResponse: SessionEventsResponse?
    var createSessionResponse: SessionCreateResponse?
    var endSessionResponse: SessionEndResponse?
    var tasksResponse: MobileTasksResponse?
    var workflowsResponse: MobileWorkflowsResponse?
    var workflowDetailResponse: MobileWorkflowDetailResponse?
    var presenceResponse: MobilePresenceResponse?
    var pipelinesResponse: MobilePipelinesResponse?
    var memoryStatsResponse: MobileMemoryStatsResponse?
    var memoryItemsResponse: MobileMemoryItemsResponse?
    var streamResponse: MobileStreamResponse?
    var topologyResponse: MobileTopologyResponse?
    var graphStatsResponse: MobileGraphStatsResponse?
    var graphEntitiesResponse: MobileGraphEntitiesResponse?
    var graphPathResponse: MobileGraphPathResponse?
    var reasoningChainsResponse: MobileReasoningChainsResponse?
    var reasoningChainDetailResponse: MobileReasoningChainDetailResponse?
    var controlPlaneResponse: MobileControlPlaneResponse?
    var alertPolicyResponse: MobileAlertPolicyResponse?
    var pushRegistrationResponse: PushRegistrationResponse?
    var pushUnregisterResponse: PushUnregisterResponse?
    var sandboxResponse: MobileSandboxSummary?
    var sandboxStartResponse: MobileSandboxStartResponse?
    var sandboxStopResponse: MobileSandboxStopResponse?
    var spawnTelemetryResponse: SpawnTelemetryResponse?
    var spawnTelemetryToolsResponse: SpawnTelemetryToolsPage?
    var spawnTelemetryFilesResponse: SpawnTelemetryFilesPage?
    var spawnTelemetryErrorsResponse: SpawnTelemetryErrorsPage?
    var spawnControlAckResponse: SpawnControlAck?
    var spawnConfigResponse: SpawnConfig?
    var millsPipelineRunsResponse: [MillsPipelineRun]?
    var millsKPIResponse: MillsKPISnapshot?
    var weaverStatusResponse: WeaverStatus?
    var weaverHistoryResponse: WeaverHistoryResponse?
    var weaverMetricsResponse: WeaverMetrics?
    var aimodelsRolesResponse: AIModelRolesResponse?

    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        if let specificError = endpointFailures[endpoint.path] {
            throw specificError
        }
        if shouldFail {
            throw failError
        }

        switch endpoint {
        case .dashboard:
            if let r = dashboardResponse as? T { return r }
        case .sessions:
            if let r = sessionsResponse as? T { return r }
        case .controlPlane:
            if let r = controlPlaneResponse as? T { return r }
        case .alertsPolicy:
            if let r = alertPolicyResponse as? T { return r }
        case .sessionDetail:
            if let r = sessionDetailResponse as? T { return r }
        case .sessionEvents:
            if let r = sessionEventsResponse as? T { return r }
        case .tasks:
            if let r = tasksResponse as? T { return r }
        case .workflows:
            if let r = workflowsResponse as? T { return r }
        case .workflowDetail:
            if let r = workflowDetailResponse as? T { return r }
        case .presence:
            if let r = presenceResponse as? T { return r }
        case .pipelines:
            if let r = pipelinesResponse as? T { return r }
        case .memoryStats:
            if let r = memoryStatsResponse as? T { return r }
        case .memoryItems:
            if let r = memoryItemsResponse as? T { return r }
        case .stream:
            if let r = streamResponse as? T { return r }
        case .topology:
            if let r = topologyResponse as? T { return r }
        case .graphStats:
            if let r = graphStatsResponse as? T { return r }
        case .graphEntities:
            if let r = graphEntitiesResponse as? T { return r }
        case .graphPath:
            if let r = graphPathResponse as? T { return r }
        case .reasoningChains:
            if let r = reasoningChainsResponse as? T { return r }
        case .reasoningChainDetail:
            if let r = reasoningChainDetailResponse as? T { return r }
        case .createSession:
            if let r = createSessionResponse as? T { return r }
        case .endSession:
            if let r = endSessionResponse as? T { return r }
        case .pushRegister:
            if let r = pushRegistrationResponse as? T { return r }
        case .pushUnregister:
            if let r = pushUnregisterResponse as? T { return r }
        case .sandbox:
            if let r = sandboxResponse as? T { return r }
        case .sandboxStart:
            if let r = sandboxStartResponse as? T { return r }
        case .sandboxStop:
            if let r = sandboxStopResponse as? T { return r }
        case .spawnTelemetry:
            if let r = spawnTelemetryResponse as? T { return r }
        case .spawnTelemetryTools:
            if let r = spawnTelemetryToolsResponse as? T { return r }
        case .spawnTelemetryFiles:
            if let r = spawnTelemetryFilesResponse as? T { return r }
        case .spawnTelemetryErrors:
            if let r = spawnTelemetryErrorsResponse as? T { return r }
        case .spawnSendMessage, .spawnInterrupt:
            if let r = spawnControlAckResponse as? T { return r }
        case .spawnConfig:
            if let r = spawnConfigResponse as? T { return r }
        case .spawnAgent, .spawnList, .spawnDetail, .spawnStop,
             .agents, .workflowApprove, .workflowReject, .handoffs, .namespaces:
            break
        case .audit, .ping, .eventsStream:
            break
        case .millsPipelineRuns:
            if let r = millsPipelineRunsResponse as? T { return r }
        case .millsKPIs:
            if let r = millsKPIResponse as? T { return r }
        case .weaverStatus:
            if let r = weaverStatusResponse as? T { return r }
        case .weaverHistory:
            if let r = weaverHistoryResponse as? T { return r }
        case .weaverMetrics:
            if let r = weaverMetricsResponse as? T { return r }
        case .aimodelsRoles:
            if let r = aimodelsRolesResponse as? T { return r }
        }

        throw LoomAPIError.noToken
    }
}
