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

    @ObservationIgnored
    private let apiClient: any LoomAPIClientProtocol

    public init(apiClient: any LoomAPIClientProtocol) {
        self.apiClient = apiClient
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
}
