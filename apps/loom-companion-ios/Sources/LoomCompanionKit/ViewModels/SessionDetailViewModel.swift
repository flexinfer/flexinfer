import Foundation

/// ViewModel for the session detail screen.
@Observable
public final class SessionDetailViewModel {
    public var session: SessionInfo?
    public var events: [TimelineEntry] = []
    public var entryBreakdown: [EntryTypeBucket] = []
    public var topEntries: [SessionTopEntry] = []
    public var decisions: [SessionTopEntry] = []
    public var errors: [SessionTopEntry] = []
    public var topFiles: [TouchedFile] = []
    public var tasks: SessionTaskSummary?
    public var activity: SessionActivityResponse?
    public var isLoading = false
    public var error: LoomAPIError?

    // Session end state
    public var isEnding = false
    public var endError: String?
    public var sessionEnded = false

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    /// Fetch session detail and events concurrently.
    public func load(sessionId: String) async {
        isLoading = true
        defer { isLoading = false }

        do {
            async let detailResult: SessionDetailResponse = apiClient.request(.sessionDetail(id: sessionId))
            async let eventsResult: SessionEventsResponse = apiClient.request(.sessionEvents(id: sessionId, limit: 100))
            let (detail, sessionEvents) = try await (detailResult, eventsResult)
            session = detail.session
            events = sessionEvents.events
            entryBreakdown = detail.entryBreakdown ?? []
            topEntries = detail.topEntries ?? []
            decisions = detail.decisions ?? []
            self.errors = detail.errors ?? []
            topFiles = detail.topFiles ?? []
            tasks = detail.tasks
            activity = try? await apiClient.request(.sessionActivity(id: sessionId))
            error = nil
        } catch let err as LoomAPIError {
            error = err
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
        }
    }

    /// End the current session.
    public func endSession(summarize: Bool = false) async {
        guard let sessionId = session?.id else { return }
        isEnding = true
        endError = nil
        defer { isEnding = false }

        do {
            let _: SessionEndResponse = try await apiClient.request(
                .endSession(id: sessionId, summarize: summarize)
            )
            session?.status = .ended
            sessionEnded = true
        } catch let err as LoomAPIError {
            endError = err.description
        } catch {
            endError = error.localizedDescription
        }
    }
}
