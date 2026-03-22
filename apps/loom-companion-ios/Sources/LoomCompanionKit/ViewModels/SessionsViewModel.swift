import Foundation

/// ViewModel for the sessions list screen.
@Observable
public final class SessionsViewModel {
    public var sessions: [SessionInfo] = []
    public var isLoading = false
    public var error: LoomAPIError?

    // Filters
    public var statusFilter: SessionStatus?
    public var agentFilter: String?
    public var searchText: String = ""

    // Namespace data
    public var availableNamespaces: [NamespaceSummary] = []

    // Namespace filter
    public var namespaceFilter: String?

    // Session creation state
    public var isCreating = false
    public var createError: String?

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    /// Fetch all sessions from the API.
    public func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let response: SessionsResponse = try await apiClient.request(.sessions)
            sessions = response.sessions
            error = nil
        } catch let err as LoomAPIError {
            error = err
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
        }
    }

    /// Fetch available namespaces from the API.
    public func loadNamespaces() async {
        do {
            let response: NamespacesResponse = try await apiClient.request(.namespaces)
            availableNamespaces = response.namespaces
        } catch {
            // Non-critical; keep stale data.
        }
    }

    /// Create a new session and reload the sessions list on success.
    public func createSession(
        agentId: String,
        namespace: String? = nil,
        description: String? = nil,
        autoRecall: Bool? = nil
    ) async {
        isCreating = true
        createError = nil
        defer { isCreating = false }

        do {
            let _: SessionCreateResponse = try await apiClient.request(
                .createSession(agentId: agentId, namespace: namespace, description: description, autoRecall: autoRecall)
            )
            await load()
        } catch let err as LoomAPIError {
            createError = err.description
        } catch {
            createError = error.localizedDescription
        }
    }

    /// Sessions after applying current filters.
    public var filteredSessions: [SessionInfo] {
        sessions.filter { session in
            if let statusFilter {
                switch statusFilter {
                case .ended:
                    if !session.status.isTerminal {
                        return false
                    }
                default:
                    if session.status != statusFilter {
                        return false
                    }
                }
            }
            if let agentFilter, !agentFilter.isEmpty, session.agentId != agentFilter {
                return false
            }
            if let namespaceFilter, !namespaceFilter.isEmpty, session.namespace != namespaceFilter {
                return false
            }
            if !searchText.isEmpty {
                let query = searchText.lowercased()
                let matches = session.namespace.lowercased().contains(query)
                    || session.description.lowercased().contains(query)
                    || session.agentId.lowercased().contains(query)
                    || session.id.lowercased().contains(query)
                if !matches { return false }
            }
            return true
        }
    }

    /// Unique agent IDs from current sessions, for filter dropdown.
    public var availableAgents: [String] {
        Array(Set(sessions.map(\.agentId))).sorted()
    }

    /// Unique namespaces from current sessions, for filter dropdown.
    public var uniqueNamespaces: [String] {
        Array(Set(sessions.map(\.namespace).filter { !$0.isEmpty })).sorted()
    }
}
