import Testing
import Foundation
@testable import LoomCompanionKit

/// Decoding contract tests for `SpawnControlResponse`. The Slice 1 server
/// returns the timestamp under different keys depending on the endpoint:
///
/// - `queued_at` for `POST /api/mobile/v1/agent/spawn/{id}/message`
/// - `interrupted_at` for `POST /api/mobile/v1/agent/spawn/{id}/interrupt`
///
/// The shared client decodes both into `timestamp` so the view layer doesn't
/// have to branch on which call it just made.
@Suite("SpawnControlResponse decoding")
struct SpawnControlResponseTests {

    @Test("Decodes message ack with queued_at")
    func decodesQueuedAt() throws {
        let payload = """
        { "spawn_id": "spawn-abc", "queued_at": "2026-04-25T12:34:56Z" }
        """
        let ack = try JSONDecoder().decode(
            SpawnControlResponse.self,
            from: Data(payload.utf8)
        )
        #expect(ack.spawnId == "spawn-abc")
        #expect(ack.timestamp == "2026-04-25T12:34:56Z")
    }

    @Test("Decodes interrupt ack with interrupted_at")
    func decodesInterruptedAt() throws {
        let payload = """
        { "spawn_id": "spawn-xyz", "interrupted_at": "2026-04-25T12:35:01.500Z" }
        """
        let ack = try JSONDecoder().decode(
            SpawnControlResponse.self,
            from: Data(payload.utf8)
        )
        #expect(ack.spawnId == "spawn-xyz")
        #expect(ack.timestamp == "2026-04-25T12:35:01.500Z")
    }

    @Test("Falls back to legacy `sent` field when present")
    func decodesLegacySentField() throws {
        let payload = """
        { "spawn_id": "spawn-legacy", "sent": "2026-04-25T12:00:00Z" }
        """
        let ack = try JSONDecoder().decode(
            SpawnControlResponse.self,
            from: Data(payload.utf8)
        )
        #expect(ack.spawnId == "spawn-legacy")
        #expect(ack.timestamp == "2026-04-25T12:00:00Z")
    }

    @Test("Throws when no timestamp key is present")
    func failsWithoutTimestamp() {
        let payload = """
        { "spawn_id": "spawn-bad" }
        """
        #expect(throws: DecodingError.self) {
            _ = try JSONDecoder().decode(
                SpawnControlResponse.self,
                from: Data(payload.utf8)
            )
        }
    }

    @Test("Throws when spawn_id is missing")
    func failsWithoutSpawnID() {
        let payload = """
        { "queued_at": "2026-04-25T12:34:56Z" }
        """
        #expect(throws: DecodingError.self) {
            _ = try JSONDecoder().decode(
                SpawnControlResponse.self,
                from: Data(payload.utf8)
            )
        }
    }

    @Test("queued_at takes precedence when both message + interrupt fields appear")
    func queuedAtPrecedence() throws {
        let payload = """
        {
          "spawn_id": "spawn-both",
          "queued_at": "2026-04-25T12:00:00Z",
          "interrupted_at": "2026-04-25T13:00:00Z"
        }
        """
        let ack = try JSONDecoder().decode(
            SpawnControlResponse.self,
            from: Data(payload.utf8)
        )
        #expect(ack.timestamp == "2026-04-25T12:00:00Z")
    }

    @Test("Round-trip encode/decode preserves spawnId + timestamp")
    func roundTripPreservesValues() throws {
        let original = SpawnControlResponse(
            spawnId: "spawn-rt",
            timestamp: "2026-04-25T14:00:00Z"
        )
        let encoded = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(
            SpawnControlResponse.self,
            from: encoded
        )
        #expect(decoded == original)
    }

    @Test("SpawnControlAck legacy alias resolves to SpawnControlResponse")
    func legacyAliasResolves() throws {
        let payload = """
        { "spawn_id": "spawn-alias", "queued_at": "2026-04-25T15:00:00Z" }
        """
        let ack: SpawnControlAck = try JSONDecoder().decode(
            SpawnControlAck.self,
            from: Data(payload.utf8)
        )
        #expect(ack.spawnId == "spawn-alias")
        #expect(ack.timestamp == "2026-04-25T15:00:00Z")
    }
}
