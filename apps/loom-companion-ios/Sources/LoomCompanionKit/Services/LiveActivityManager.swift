import Foundation

#if os(iOS)
import ActivityKit

/// Manages Live Activity lifecycle for workflows and coding sessions.
/// Driven by SSE events from the HUD server.
///
/// Pool budget: max 5 concurrent activities (iOS limit).
/// - 3 slots reserved for sessions
/// - 2 slots reserved for workflows
@available(iOS 16.2, *)
@MainActor
public final class LiveActivityManager {
    public static let shared = LiveActivityManager()

    /// Maximum concurrent Live Activities (iOS limit is 5).
    private let maxConcurrent = 5
    private let maxSessionSlots = 3
    private let maxWorkflowSlots = 2

    /// Active workflow activities indexed by workflow ID.
    private var workflowActivities: [String: Activity<WorkflowActivityAttributes>] = [:]

    /// Active session activities indexed by session ID.
    private var sessionActivities: [String: Activity<SessionActivityAttributes>] = [:]

    /// Maps agent IDs to their active session IDs (populated from session.start events).
    private var agentSessionMap: [String: String] = [:]

    private init() {}

    // MARK: - Pool Management

    /// Total active Live Activities across all types.
    public var activeCount: Int {
        workflowActivities.count + sessionActivities.count + pipelineActivities.count
    }

    /// Number of active workflow Live Activities.
    public var activeWorkflowCount: Int { workflowActivities.count }

    /// Number of active session Live Activities.
    public var activeSessionCount: Int { sessionActivities.count }

    /// Whether a session has an active Live Activity.
    public func hasSessionActivity(sessionId: String) -> Bool {
        sessionActivities[sessionId] != nil
    }

    /// Whether a specific workflow has an active Live Activity.
    public func hasWorkflowActivity(workflowId: String) -> Bool {
        workflowActivities[workflowId] != nil
    }

    // MARK: - Stale Activity Reaping

    /// Reap activities that haven't been updated within their staleness window.
    /// Checks server-provided `staleAfter` timestamp first (authoritative).
    /// Falls back to ActivityKit staleDate heuristic when no server hint.
    /// Sessions: 5 minutes. Workflows/Pipelines: 10 minutes.
    public func reapStaleActivities() {
        let sessionStaleThreshold: TimeInterval = 300
        let otherStaleThreshold: TimeInterval = 600
        let isoFormatter = ISO8601DateFormatter()

        for (id, activity) in sessionActivities {
            // Server-authoritative: check staleAfter field first.
            let serverStaleAfter = activity.content.state.staleAfter
            if !serverStaleAfter.isEmpty,
               let staleDate = isoFormatter.date(from: serverStaleAfter),
               Date() > staleDate {
                endSessionActivity(sessionId: id, finalStatus: "stale")
                continue
            }

            // Fall back to ActivityKit staleDate when server hint is absent.
            if let staleDate = activity.content.staleDate, Date() > staleDate {
                endSessionActivity(sessionId: id, finalStatus: "stale")
            } else if serverStaleAfter.isEmpty,
                      activity.content.state.status == "active",
                      activity.content.state.elapsedSeconds == 0 {
                if let date = activity.content.staleDate, Date().timeIntervalSince(date) > sessionStaleThreshold {
                    endSessionActivity(sessionId: id, finalStatus: "stale")
                }
            }
        }

        for (id, activity) in workflowActivities {
            if let staleDate = activity.content.staleDate, Date().timeIntervalSince(staleDate) > otherStaleThreshold {
                endWorkflowActivity(workflowId: id, finalStatus: "stale")
            }
        }

        for (id, activity) in pipelineActivities {
            if let staleDate = activity.content.staleDate, Date().timeIntervalSince(staleDate) > otherStaleThreshold {
                endPipelineActivity(pipelineId: id, finalStatus: "stale")
            }
        }
    }

    /// End all activities. Used when the app disconnects or enters background.
    public func endAllActivities() {
        for id in Array(sessionActivities.keys) {
            endSessionActivity(sessionId: id, finalStatus: "disconnected")
        }
        for id in Array(workflowActivities.keys) {
            endWorkflowActivity(workflowId: id, finalStatus: "disconnected")
        }
        for id in Array(pipelineActivities.keys) {
            endPipelineActivity(pipelineId: id, finalStatus: "disconnected")
        }
    }

    // MARK: - Agent → Session Mapping

    /// Register an agent→session mapping from a session.start event.
    public func registerAgentSession(agentId: String, sessionId: String) {
        agentSessionMap[agentId] = sessionId
    }

    /// Look up a session ID for an agent (used to correlate heartbeat events).
    public func sessionId(forAgent agentId: String) -> String? {
        agentSessionMap[agentId]
    }

    /// Remove agent→session mapping when a session ends.
    public func unregisterAgentSession(agentId: String) {
        agentSessionMap.removeValue(forKey: agentId)
    }

    // MARK: - Workflow Activities

    /// Start a new Live Activity for a workflow.
    public func startWorkflowActivity(
        workflowId: String,
        name: String,
        agentId: String,
        initialStep: String = "Starting",
        totalSteps: Int = 1
    ) {
        guard workflowActivities[workflowId] == nil else { return }
        guard workflowActivities.count < maxWorkflowSlots else { return }
        guard activeCount < maxConcurrent else { return }
        guard ActivityAuthorizationInfo().areActivitiesEnabled else { return }

        let attributes = WorkflowActivityAttributes(
            workflowId: workflowId,
            workflowName: name,
            agentId: agentId
        )
        let state = WorkflowActivityAttributes.ContentState(
            currentStepName: initialStep,
            currentStepIndex: 0,
            totalSteps: totalSteps,
            status: "running",
            elapsedSeconds: 0
        )

        do {
            let activity = try Activity.request(
                attributes: attributes,
                content: .init(state: state, staleDate: nil),
                pushType: nil
            )
            workflowActivities[workflowId] = activity
        } catch {
            print("[LiveActivityManager] Failed to start workflow activity for \(workflowId): \(error)")
        }
    }

    /// Update a running workflow Live Activity with new state.
    public func updateWorkflowActivity(
        workflowId: String,
        stepName: String,
        stepIndex: Int,
        totalSteps: Int,
        status: String,
        elapsedSeconds: Int
    ) {
        guard let activity = workflowActivities[workflowId] else { return }

        let state = WorkflowActivityAttributes.ContentState(
            currentStepName: stepName,
            currentStepIndex: stepIndex,
            totalSteps: totalSteps,
            status: status,
            elapsedSeconds: elapsedSeconds
        )

        Task {
            await activity.update(.init(state: state, staleDate: nil))
        }
    }

    /// End a workflow Live Activity with a final state.
    public func endWorkflowActivity(
        workflowId: String,
        finalStatus: String = "completed"
    ) {
        guard let activity = workflowActivities.removeValue(forKey: workflowId) else { return }

        let currentState = activity.content.state
        let finalState = WorkflowActivityAttributes.ContentState(
            currentStepName: finalStatus == "completed" ? "Done" : "Failed",
            currentStepIndex: currentState.totalSteps,
            totalSteps: currentState.totalSteps,
            status: finalStatus,
            elapsedSeconds: currentState.elapsedSeconds
        )

        Task {
            await activity.end(
                .init(state: finalState, staleDate: nil),
                dismissalPolicy: .after(.now + 300)
            )
        }
    }

    // MARK: - Session Activities

    /// Start a new Live Activity for a coding session.
    public func startSessionActivity(
        sessionId: String,
        agentId: String,
        agentType: String,
        namespace: String,
        branch: String = "",
        currentTask: String = "",
        estimatedCost: Double = 0
    ) {
        guard sessionActivities[sessionId] == nil else { return }
        guard sessionActivities.count < maxSessionSlots else { return }
        guard activeCount < maxConcurrent else { return }
        guard ActivityAuthorizationInfo().areActivitiesEnabled else { return }

        let attributes = SessionActivityAttributes(
            sessionId: sessionId,
            agentId: agentId,
            agentType: agentType,
            namespace: namespace
        )
        let state = SessionActivityAttributes.ContentState(
            status: "active",
            currentTask: currentTask,
            branch: branch,
            elapsedSeconds: 0,
            tokenCount: 0,
            entryCount: 0,
            estimatedCost: estimatedCost
        )

        do {
            let activity = try Activity.request(
                attributes: attributes,
                content: .init(state: state, staleDate: nil),
                pushType: nil
            )
            sessionActivities[sessionId] = activity
            registerAgentSession(agentId: agentId, sessionId: sessionId)
        } catch {
            print("[LiveActivityManager] Failed to start session activity for \(sessionId): \(error)")
        }
    }

    /// Update a running session Live Activity (typically from heartbeat events).
    public func updateSessionActivity(
        sessionId: String,
        status: String = "active",
        currentTask: String? = nil,
        branch: String? = nil,
        tokenCount: Int? = nil,
        entryCount: Int? = nil,
        estimatedCost: Double? = nil,
        staleAfter: String? = nil
    ) {
        guard let activity = sessionActivities[sessionId] else { return }

        let currentState = activity.content.state
        let resolvedStaleAfter = staleAfter ?? currentState.staleAfter
        let state = SessionActivityAttributes.ContentState(
            status: status,
            currentTask: currentTask ?? currentState.currentTask,
            branch: branch ?? currentState.branch,
            elapsedSeconds: 0, // Timer is handled by Text(startDate, style: .timer)
            tokenCount: tokenCount ?? currentState.tokenCount,
            entryCount: entryCount ?? currentState.entryCount,
            estimatedCost: estimatedCost ?? currentState.estimatedCost,
            staleAfter: resolvedStaleAfter
        )

        // Derive ActivityKit staleDate from the server-provided timestamp.
        var activityStaleDate: Date?
        if !resolvedStaleAfter.isEmpty {
            activityStaleDate = ISO8601DateFormatter().date(from: resolvedStaleAfter)
        }

        Task {
            await activity.update(.init(state: state, staleDate: activityStaleDate))
        }
    }

    /// End a session Live Activity.
    public func endSessionActivity(
        sessionId: String,
        finalStatus: String = "ended"
    ) {
        guard let activity = sessionActivities.removeValue(forKey: sessionId) else { return }

        // Clean up agent→session mapping.
        let agentId = activity.attributes.agentId
        unregisterAgentSession(agentId: agentId)

        let currentState = activity.content.state
        let finalState = SessionActivityAttributes.ContentState(
            status: finalStatus,
            currentTask: "",
            branch: currentState.branch,
            elapsedSeconds: 0,
            tokenCount: currentState.tokenCount,
            entryCount: currentState.entryCount,
            estimatedCost: currentState.estimatedCost,
            staleAfter: ""
        )

        Task {
            await activity.end(
                .init(state: finalState, staleDate: nil),
                dismissalPolicy: .after(.now + 120)
            )
        }
    }

    /// End a session Live Activity by agent ID (using the agent→session map).
    public func endSessionActivityByAgent(agentId: String, finalStatus: String = "ended") {
        guard let sessionId = agentSessionMap[agentId] else { return }
        endSessionActivity(sessionId: sessionId, finalStatus: finalStatus)
    }

    // MARK: - Pipeline Activities

    /// Active pipeline activities indexed by pipeline ID.
    private var pipelineActivities: [Int: Activity<PipelineActivityAttributes>] = [:]

    /// Number of active pipeline Live Activities.
    public var activePipelineCount: Int { pipelineActivities.count }

    /// Whether a specific pipeline has an active Live Activity.
    public func hasPipelineActivity(pipelineId: Int) -> Bool {
        pipelineActivities[pipelineId] != nil
    }

    /// Start a new Live Activity for a CI pipeline.
    public func startPipelineActivity(
        pipelineId: Int,
        project: String,
        ref: String,
        currentStage: String = "Starting",
        totalStages: Int = 1,
        agentId: String = "",
        agentType: String = ""
    ) {
        guard pipelineActivities[pipelineId] == nil else { return }
        guard activeCount < maxConcurrent else { return }
        guard ActivityAuthorizationInfo().areActivitiesEnabled else { return }

        let attributes = PipelineActivityAttributes(
            pipelineId: pipelineId,
            project: project,
            ref: ref,
            agentId: agentId,
            agentType: agentType
        )
        let state = PipelineActivityAttributes.ContentState(
            status: "running",
            currentStage: currentStage,
            completedStages: 0,
            totalStages: totalStages,
            failedJobCount: 0,
            elapsedSeconds: 0
        )

        do {
            let activity = try Activity.request(
                attributes: attributes,
                content: .init(state: state, staleDate: nil),
                pushType: nil
            )
            pipelineActivities[pipelineId] = activity
        } catch {
            print("[LiveActivityManager] Failed to start pipeline activity for \(pipelineId): \(error)")
        }
    }

    /// Update a running pipeline Live Activity.
    public func updatePipelineActivity(
        pipelineId: Int,
        status: String,
        currentStage: String,
        completedStages: Int,
        totalStages: Int,
        failedJobCount: Int
    ) {
        guard let activity = pipelineActivities[pipelineId] else { return }

        let state = PipelineActivityAttributes.ContentState(
            status: status,
            currentStage: currentStage,
            completedStages: completedStages,
            totalStages: totalStages,
            failedJobCount: failedJobCount,
            elapsedSeconds: 0
        )

        Task {
            await activity.update(.init(state: state, staleDate: nil))
        }
    }

    /// End a pipeline Live Activity.
    public func endPipelineActivity(
        pipelineId: Int,
        finalStatus: String = "success"
    ) {
        guard let activity = pipelineActivities.removeValue(forKey: pipelineId) else { return }

        let currentState = activity.content.state
        let finalState = PipelineActivityAttributes.ContentState(
            status: finalStatus,
            currentStage: finalStatus == "success" ? "Done" : currentState.currentStage,
            completedStages: currentState.totalStages,
            totalStages: currentState.totalStages,
            failedJobCount: currentState.failedJobCount,
            elapsedSeconds: 0
        )

        Task {
            await activity.end(
                .init(state: finalState, staleDate: nil),
                dismissalPolicy: .after(.now + 180)
            )
        }
    }
}
#endif
