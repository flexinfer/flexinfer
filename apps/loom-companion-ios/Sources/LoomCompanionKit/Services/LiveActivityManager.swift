import Foundation

#if os(iOS)
import ActivityKit

/// Manages Live Activity lifecycle for running workflows.
/// Driven by SSE events from the HUD server.
@available(iOS 16.2, *)
@MainActor
public final class LiveActivityManager {
    public static let shared = LiveActivityManager()

    /// Maximum concurrent Live Activities (iOS limit is 5).
    private let maxConcurrent = 5

    /// Active activities indexed by workflow ID.
    private var activities: [String: Activity<WorkflowActivityAttributes>] = [:]

    private init() {}

    /// Start a new Live Activity for a workflow.
    public func startWorkflowActivity(
        workflowId: String,
        name: String,
        agentId: String,
        initialStep: String = "Starting",
        totalSteps: Int = 1
    ) {
        guard activities[workflowId] == nil else { return }
        guard activities.count < maxConcurrent else { return }
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
            activities[workflowId] = activity
        } catch {
            print("[LiveActivityManager] Failed to start activity for \(workflowId): \(error)")
        }
    }

    /// Update a running Live Activity with new state.
    public func updateWorkflowActivity(
        workflowId: String,
        stepName: String,
        stepIndex: Int,
        totalSteps: Int,
        status: String,
        elapsedSeconds: Int
    ) {
        guard let activity = activities[workflowId] else { return }

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

    /// End a Live Activity with a final state.
    public func endWorkflowActivity(
        workflowId: String,
        finalStatus: String = "completed"
    ) {
        guard let activity = activities.removeValue(forKey: workflowId) else { return }

        let finalState = WorkflowActivityAttributes.ContentState(
            currentStepName: finalStatus == "completed" ? "Done" : "Failed",
            currentStepIndex: (try? activity.content.state.totalSteps) ?? 1,
            totalSteps: (try? activity.content.state.totalSteps) ?? 1,
            status: finalStatus,
            elapsedSeconds: (try? activity.content.state.elapsedSeconds) ?? 0
        )

        Task {
            await activity.end(
                .init(state: finalState, staleDate: nil),
                dismissalPolicy: .after(.now + 300)
            )
        }
    }

    /// Number of active Live Activities.
    public var activeCount: Int { activities.count }
}
#endif
