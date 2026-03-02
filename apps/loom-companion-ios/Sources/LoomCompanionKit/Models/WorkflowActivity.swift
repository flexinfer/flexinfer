import Foundation

#if os(iOS)
import ActivityKit

/// ActivityAttributes for tracking a running workflow on the lock screen and Dynamic Island.
@available(iOS 16.2, *)
public struct WorkflowActivityAttributes: ActivityAttributes {
    public struct ContentState: Codable, Hashable {
        public let currentStepName: String
        public let currentStepIndex: Int
        public let totalSteps: Int
        public let status: String
        public let elapsedSeconds: Int

        public init(currentStepName: String, currentStepIndex: Int, totalSteps: Int, status: String, elapsedSeconds: Int) {
            self.currentStepName = currentStepName
            self.currentStepIndex = currentStepIndex
            self.totalSteps = totalSteps
            self.status = status
            self.elapsedSeconds = elapsedSeconds
        }

        public var progress: Double {
            guard totalSteps > 0 else { return 0 }
            return Double(currentStepIndex) / Double(totalSteps)
        }

        public var isComplete: Bool {
            status == "completed" || status == "failed" || status == "cancelled"
        }
    }

    public let workflowId: String
    public let workflowName: String
    public let agentId: String

    public init(workflowId: String, workflowName: String, agentId: String) {
        self.workflowId = workflowId
        self.workflowName = workflowName
        self.agentId = agentId
    }
}
#endif
