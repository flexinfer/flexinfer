import Testing
import Foundation
@testable import LoomCompanionKit

/// Behavioural tests for the multi-turn control plane on `SpawnViewModel`.
///
/// The view layer (`SpawnDetailView`) calls these throwing methods and
/// surfaces errors as alerts, so each test pins down (a) the happy path
/// returning a populated `SpawnControlResponse` and (b) the failure path
/// re-raising the underlying `LoomAPIError` so the view can read its
/// description for the alert body.
@Suite("SpawnViewModel multi-turn")
struct SpawnViewModelTests {

    @Test("sendMessage returns ack on success")
    func sendMessageHappyPath() async throws {
        let client = MockAPIClient()
        client.spawnControlAckResponse = SpawnControlResponse(
            spawnId: "spawn-happy",
            timestamp: "2026-04-25T12:00:00Z"
        )
        let vm = SpawnViewModel(apiClient: client)

        let ack = try await vm.sendMessage(
            spawnId: "spawn-happy",
            message: "please continue"
        )

        #expect(ack.spawnId == "spawn-happy")
        #expect(ack.timestamp == "2026-04-25T12:00:00Z")
    }

    @Test("sendMessage rethrows LoomAPIError so the view can show response body")
    func sendMessageRethrowsAPIError() async {
        let client = MockAPIClient()
        client.endpointFailures["/api/mobile/v1/agent/spawn/spawn-bad/message"] =
            .apiError(code: .badRequest, message: "text is required", requestId: "req-1")
        let vm = SpawnViewModel(apiClient: client)

        await #expect(throws: LoomAPIError.self) {
            _ = try await vm.sendMessage(spawnId: "spawn-bad", message: "")
        }
    }

    @Test("sendMessage surfaces server-supplied message in error description")
    func sendMessageSurfacesServerMessage() async {
        let client = MockAPIClient()
        // The HUD returns 409 with a structured envelope; mock it by routing
        // the endpoint to a `bad_request` error (the closest mappable code in
        // `APIErrorCode`) and verify the view-model preserves the message
        // text so the alert body in the view shows what the user did wrong.
        client.endpointFailures["/api/mobile/v1/agent/spawn/spawn-stopped/message"] =
            .apiError(code: .badRequest, message: "spawn not in running state", requestId: "req-2")
        let vm = SpawnViewModel(apiClient: client)

        do {
            _ = try await vm.sendMessage(spawnId: "spawn-stopped", message: "hi")
            Issue.record("expected throw")
        } catch let err as LoomAPIError {
            // The view formats this into the alert body — make sure the
            // server-supplied message survives the round-trip.
            #expect(err.description.contains("spawn not in running state"))
        } catch {
            Issue.record("unexpected error: \(error)")
        }
    }

    @Test("interruptSpawn returns ack on success")
    func interruptSpawnHappyPath() async throws {
        let client = MockAPIClient()
        client.spawnControlAckResponse = SpawnControlResponse(
            spawnId: "spawn-int",
            timestamp: "2026-04-25T12:05:00Z"
        )
        let vm = SpawnViewModel(apiClient: client)

        let ack = try await vm.interruptSpawn(id: "spawn-int")

        #expect(ack.spawnId == "spawn-int")
        #expect(ack.timestamp == "2026-04-25T12:05:00Z")
    }

    @Test("interruptSpawn rethrows on 404")
    func interruptSpawnRethrows404() async {
        let client = MockAPIClient()
        client.endpointFailures["/api/mobile/v1/agent/spawn/missing/interrupt"] =
            .apiError(code: .notFound, message: "spawn missing not found", requestId: "req-3")
        let vm = SpawnViewModel(apiClient: client)

        await #expect(throws: LoomAPIError.self) {
            _ = try await vm.interruptSpawn(id: "missing")
        }
    }
}
