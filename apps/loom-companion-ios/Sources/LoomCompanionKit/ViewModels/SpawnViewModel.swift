import Foundation

/// ViewModel for the agent spawn UI.
@Observable
public final class SpawnViewModel {
    public var spawns: [MobileSpawnStatus] = []
    public var config: SpawnConfig?
    public var isLoading = false
    public var isLoadingConfig = false
    public var isSpawning = false
    public var error: LoomAPIError?

    /// Exposed so sibling views (e.g. SpawnDetailView) can reuse the same
    /// client for telemetry sub-requests without requiring a separate
    /// injection path from `OpsView`.
    @ObservationIgnored
    public let apiClient: any LoomAPIClientProtocol

    @ObservationIgnored
    private var sseRegistrationId: UUID?

    /// Live stream events for the detail view (e.g. tool_start, message).
    public var liveEvents: [SSEEvent] = []

    /// SSE event types that trigger a spawn list refresh.
    private static let refreshEventTypes: Set<String> = [
        "agent.spawn.building",
        "agent.spawn.running",
        "agent.spawn.completed",
        "agent.spawn.failed",
        "agent.spawn.stopped",
    ]

    /// SSE event types that should be surfaced in the live stream.
    private static let liveStreamEventTypes: Set<String> = [
        "agent.spawn.tool_start",
        "agent.spawn.tool_complete",
        "agent.spawn.message",
        "agent.spawn.thinking",
        "agent.spawn.partial_message"
    ]

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
    }

    /// Start listening to SSE events via the broadcaster for real-time spawn updates.
    @MainActor
    public func startListening(broadcaster: SSEEventBroadcaster) {
        sseRegistrationId = broadcaster.register { [weak self] event in
            await self?.handleSSEEvent(event)
        }
    }

    /// Stop listening to SSE events.
    @MainActor
    public func stopListening(broadcaster: SSEEventBroadcaster) {
        if let id = sseRegistrationId {
            broadcaster.unregister(id)
            sseRegistrationId = nil
        }
    }

    @MainActor
    private func handleSSEEvent(_ event: SSEEvent) async {
        if Self.refreshEventTypes.contains(event.type) {
            await loadSpawns()
        } else if Self.liveStreamEventTypes.contains(event.type) {
            liveEvents.append(event)
            if liveEvents.count > 200 {
                liveEvents.removeFirst(liveEvents.count - 200)
            }
        }
    }

    /// Load spawn configuration (projects, agent types, defaults).
    public func loadConfig() async {
        isLoadingConfig = true
        defer { isLoadingConfig = false }
        do {
            config = try await apiClient.request(.spawnConfig)
        } catch {
            // Config is optional — fall back to hardcoded defaults on failure.
        }
    }

    /// Load the list of active and recent spawns.
    public func loadSpawns() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let response: MobileSpawnListResponse = try await apiClient.request(.spawnList)
            spawns = response.spawns
            error = nil
        } catch let err as LoomAPIError {
            error = err
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
        }
    }

    /// Spawn a new headless agent.
    public func spawnAgent(
        agentType: AgentType,
        project: String,
        branch: String,
        taskDescription: String
    ) async -> MobileSpawnResponse? {
        isSpawning = true
        defer { isSpawning = false }
        do {
            let request = MobileSpawnRequest(
                agentType: agentType,
                project: project,
                branch: branch.isEmpty ? nil : branch,
                taskDescription: taskDescription
            )
            let response: MobileSpawnResponse = try await apiClient.request(.spawnAgent(request: request))
            error = nil
            await loadSpawns()
            return response
        } catch let err as LoomAPIError {
            error = err
            return nil
        } catch {
            self.error = .networkError(underlying: error.localizedDescription)
            return nil
        }
    }

    /// Stop a running spawn.
    public func stopSpawn(id: String) async {
        struct StopResponse: Decodable {
            let stopped: Bool
            let spawnId: String

            enum CodingKeys: String, CodingKey {
                case stopped
                case spawnId = "spawn_id"
            }
        }
        do {
            let _: StopResponse = try await apiClient.request(.spawnStop(id: id))
            await loadSpawns()
        } catch {
            // Refresh list anyway.
            await loadSpawns()
        }
    }

    /// Send a follow-up message to a multi-turn spawn.
    public func sendMessage(spawnId: String, message: String) async -> Bool {
        do {
            let _: SpawnControlAck = try await apiClient.request(.spawnSendMessage(id: spawnId, text: message))
            return true
        } catch {
            return false
        }
    }

    /// Interrupt a running spawn.
    public func interruptSpawn(id: String) async -> Bool {
        do {
            let _: SpawnControlAck = try await apiClient.request(.spawnInterrupt(id: id))
            return true
        } catch {
            return false
        }
    }
}
