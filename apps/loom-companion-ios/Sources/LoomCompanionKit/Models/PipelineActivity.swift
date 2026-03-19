import Foundation

#if os(iOS)
import ActivityKit

/// ActivityAttributes for tracking a GitLab CI pipeline on the lock screen and Dynamic Island.
@available(iOS 16.2, *)
public struct PipelineActivityAttributes: ActivityAttributes {
    public struct ContentState: Codable, Hashable {
        public let status: String
        public let currentStage: String
        public let completedStages: Int
        public let totalStages: Int
        public let failedJobCount: Int
        public let elapsedSeconds: Int

        public init(
            status: String,
            currentStage: String,
            completedStages: Int,
            totalStages: Int,
            failedJobCount: Int,
            elapsedSeconds: Int
        ) {
            self.status = status
            self.currentStage = currentStage
            self.completedStages = completedStages
            self.totalStages = totalStages
            self.failedJobCount = failedJobCount
            self.elapsedSeconds = elapsedSeconds
        }

        public var progress: Double {
            guard totalStages > 0 else { return 0 }
            return Double(completedStages) / Double(totalStages)
        }

        public var isComplete: Bool {
            ["success", "failed", "canceled"].contains(status)
        }

        public var stageFraction: String {
            "\(completedStages)/\(totalStages)"
        }
    }

    public let pipelineId: Int
    public let project: String
    public let ref: String
    public let startDate: Date

    public init(
        pipelineId: Int,
        project: String,
        ref: String,
        startDate: Date = .now
    ) {
        self.pipelineId = pipelineId
        self.project = project
        self.ref = ref
        self.startDate = startDate
    }
}
#endif
