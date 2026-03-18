import Foundation

#if os(iOS)
import ActivityKit

/// ActivityAttributes for tracking an active coding session on the lock screen and Dynamic Island.
@available(iOS 16.2, *)
public struct SessionActivityAttributes: ActivityAttributes {
    public struct ContentState: Codable, Hashable {
        public let status: String
        public let currentTask: String
        public let branch: String
        public let elapsedSeconds: Int
        public let tokenCount: Int
        public let entryCount: Int

        public init(
            status: String,
            currentTask: String,
            branch: String,
            elapsedSeconds: Int,
            tokenCount: Int,
            entryCount: Int
        ) {
            self.status = status
            self.currentTask = currentTask
            self.branch = branch
            self.elapsedSeconds = elapsedSeconds
            self.tokenCount = tokenCount
            self.entryCount = entryCount
        }

        public var isEnded: Bool {
            status == "ended" || status == "summarized"
        }
    }

    public let sessionId: String
    public let agentId: String
    public let agentType: String
    public let namespace: String
    public let startDate: Date

    public init(
        sessionId: String,
        agentId: String,
        agentType: String,
        namespace: String,
        startDate: Date = .now
    ) {
        self.sessionId = sessionId
        self.agentId = agentId
        self.agentType = agentType
        self.namespace = namespace
        self.startDate = startDate
    }
}
#endif
