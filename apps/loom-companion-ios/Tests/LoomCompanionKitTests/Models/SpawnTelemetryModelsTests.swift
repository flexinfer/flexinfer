import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("SpawnTelemetry Decoding")
struct SpawnTelemetryModelsTests {

    /// Captured-style payload exercising every field, including pointer-semantic
    /// `exit_code: 0`, optionals being absent, and nested arrays/maps.
    private static let fullPayload = """
    {
      "external_session_id": "sess_abc123",
      "turn_count": 4,
      "total_cost_usd": 0.1234,
      "token_usage": {
        "input_tokens": 1500,
        "output_tokens": 850,
        "cache_creation_tokens": 200,
        "cache_read_tokens": 4096
      },
      "model_usage": {
        "claude-opus-4-6": {
          "cost_usd": 0.10,
          "input_tokens": 1200,
          "output_tokens": 700
        },
        "claude-haiku-4-5": {
          "cost_usd": 0.0234,
          "input_tokens": 300,
          "output_tokens": 150
        }
      },
      "tool_calls": [
        {
          "name": "Bash",
          "server_name": "builtin",
          "duration_ms": 412,
          "exit_code": 0,
          "timestamp": "2026-04-07T10:00:00Z"
        },
        {
          "name": "Read",
          "duration_ms": 12,
          "timestamp": "2026-04-07T10:00:01Z"
        },
        {
          "name": "Write",
          "server_name": "builtin",
          "duration_ms": 87,
          "exit_code": 1,
          "error": "permission denied",
          "timestamp": "2026-04-07T10:00:02Z"
        }
      ],
      "file_changes": [
        { "path": "src/foo.go", "kind": "modify", "lines_added": 12, "lines_removed": 3 },
        { "path": "docs/new.md", "kind": "create" }
      ],
      "errors": [
        {
          "type": "tool_failure",
          "message": "Write failed: permission denied",
          "time": "2026-04-07T10:00:02Z"
        }
      ],
      "stop_reason": "end_turn",
      "last_message": "All done."
    }
    """

    @Test("Decodes full telemetry payload with all fields populated")
    func decodesFullPayload() throws {
        let data = Data(Self.fullPayload.utf8)
        let telemetry = try JSONDecoder().decode(SpawnTelemetry.self, from: data)

        #expect(telemetry.externalSessionID == "sess_abc123")
        #expect(telemetry.turnCount == 4)
        #expect(telemetry.totalCostUSD == 0.1234)
        #expect(telemetry.stopReason == "end_turn")
        #expect(telemetry.lastMessage == "All done.")

        // Token usage scalar fields.
        #expect(telemetry.tokenUsage.inputTokens == 1500)
        #expect(telemetry.tokenUsage.outputTokens == 850)
        #expect(telemetry.tokenUsage.cacheCreationTokens == 200)
        #expect(telemetry.tokenUsage.cacheReadTokens == 4096)

        // Model usage map.
        let modelUsage = try #require(telemetry.modelUsage)
        #expect(modelUsage.count == 2)
        let opus = try #require(modelUsage["claude-opus-4-6"])
        #expect(opus.costUSD == 0.10)
        #expect(opus.inputTokens == 1200)
        #expect(opus.outputTokens == 700)
        let haiku = try #require(modelUsage["claude-haiku-4-5"])
        #expect(haiku.costUSD == 0.0234)
        #expect(haiku.inputTokens == 300)
        #expect(haiku.outputTokens == 150)

        // Tool calls — exercises all optional permutations.
        let tools = try #require(telemetry.toolCalls)
        #expect(tools.count == 3)

        let bash = tools[0]
        #expect(bash.name == "Bash")
        #expect(bash.serverName == "builtin")
        #expect(bash.durationMs == 412)
        // Pointer-style exit_code of 0 must decode as Optional(0), NOT nil.
        #expect(bash.exitCode == 0)
        #expect(bash.exitCode != nil)
        #expect(bash.error == nil)
        #expect(bash.timestamp == "2026-04-07T10:00:00Z")

        let read = tools[1]
        #expect(read.name == "Read")
        #expect(read.serverName == nil)
        #expect(read.durationMs == 12)
        #expect(read.exitCode == nil)
        #expect(read.error == nil)

        let write = tools[2]
        #expect(write.name == "Write")
        #expect(write.exitCode == 1)
        #expect(write.error == "permission denied")

        // File changes — first entry exercises lines_added/lines_removed,
        // second exercises the omitempty default-to-zero path.
        let files = try #require(telemetry.fileChanges)
        #expect(files.count == 2)
        #expect(files[0].path == "src/foo.go")
        #expect(files[0].kind == "modify")
        #expect(files[0].linesAdded == 12)
        #expect(files[0].linesRemoved == 3)
        #expect(files[1].path == "docs/new.md")
        #expect(files[1].kind == "create")
        #expect(files[1].linesAdded == 0)
        #expect(files[1].linesRemoved == 0)

        // Errors.
        let errors = try #require(telemetry.errors)
        #expect(errors.count == 1)
        #expect(errors[0].type == "tool_failure")
        #expect(errors[0].message == "Write failed: permission denied")
        #expect(errors[0].time == "2026-04-07T10:00:02Z")
    }

    @Test("Optional fields default to nil when absent in JSON")
    func decodesMinimalPayload() throws {
        let payload = """
        {
          "turn_count": 0,
          "total_cost_usd": 0,
          "token_usage": {
            "input_tokens": 0,
            "output_tokens": 0,
            "cache_creation_tokens": 0,
            "cache_read_tokens": 0
          }
        }
        """
        let telemetry = try JSONDecoder().decode(SpawnTelemetry.self, from: Data(payload.utf8))

        #expect(telemetry.externalSessionID == nil)
        #expect(telemetry.turnCount == 0)
        #expect(telemetry.totalCostUSD == 0)
        #expect(telemetry.modelUsage == nil)
        #expect(telemetry.toolCalls == nil)
        #expect(telemetry.fileChanges == nil)
        #expect(telemetry.errors == nil)
        #expect(telemetry.stopReason == nil)
        #expect(telemetry.lastMessage == nil)
    }

    @Test("exit_code: 0 decodes as Optional(0), not nil")
    func decodesZeroExitCodeAsPresentOptional() throws {
        let payload = """
        {
          "name": "Bash",
          "duration_ms": 5,
          "exit_code": 0,
          "timestamp": "2026-04-07T10:00:00Z"
        }
        """
        let tool = try JSONDecoder().decode(SpawnToolCall.self, from: Data(payload.utf8))
        #expect(tool.exitCode != nil)
        #expect(tool.exitCode == 0)
    }

    @Test("Missing exit_code decodes as nil")
    func decodesMissingExitCodeAsNil() throws {
        let payload = """
        {
          "name": "Read",
          "timestamp": "2026-04-07T10:00:00Z"
        }
        """
        let tool = try JSONDecoder().decode(SpawnToolCall.self, from: Data(payload.utf8))
        #expect(tool.exitCode == nil)
        #expect(tool.serverName == nil)
        #expect(tool.durationMs == nil)
        #expect(tool.error == nil)
    }

    @Test("SpawnFileChange decodes line deltas, defaults to zero when absent")
    func decodesFileChangeLineDeltas() throws {
        let withDeltas = """
        { "path": "src/a.go", "kind": "modify", "lines_added": 7, "lines_removed": 2 }
        """
        let withoutDeltas = """
        { "path": "src/b.go", "kind": "create" }
        """
        let a = try JSONDecoder().decode(SpawnFileChange.self, from: Data(withDeltas.utf8))
        let b = try JSONDecoder().decode(SpawnFileChange.self, from: Data(withoutDeltas.utf8))

        #expect(a.linesAdded == 7)
        #expect(a.linesRemoved == 2)
        #expect(b.linesAdded == 0)
        #expect(b.linesRemoved == 0)

        // Encoded keys must match the Go server's snake_case contract.
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let json = try #require(String(data: try encoder.encode(a), encoding: .utf8))
        #expect(json.contains("\"lines_added\":7"))
        #expect(json.contains("\"lines_removed\":2"))
    }

    @Test("Round-trip encode/decode preserves equality")
    func roundTripPreservesEquality() throws {
        let original = try JSONDecoder().decode(SpawnTelemetry.self, from: Data(Self.fullPayload.utf8))

        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let encoded = try encoder.encode(original)

        let decoded = try JSONDecoder().decode(SpawnTelemetry.self, from: encoded)
        #expect(decoded == original)
    }

    @Test("Encoded keys use snake_case to match Go server contract")
    func encodesSnakeCaseKeys() throws {
        let telemetry = SpawnTelemetry(
            externalSessionID: "sess_x",
            turnCount: 1,
            totalCostUSD: 0.5,
            tokenUsage: SpawnTokenUsage(
                inputTokens: 10,
                outputTokens: 20,
                cacheCreationTokens: 0,
                cacheReadTokens: 0
            ),
            stopReason: "end_turn"
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let data = try encoder.encode(telemetry)
        let json = try #require(String(data: data, encoding: .utf8))

        #expect(json.contains("\"external_session_id\""))
        #expect(json.contains("\"turn_count\""))
        #expect(json.contains("\"total_cost_usd\""))
        #expect(json.contains("\"token_usage\""))
        #expect(json.contains("\"input_tokens\""))
        #expect(json.contains("\"cache_creation_tokens\""))
        #expect(json.contains("\"stop_reason\""))
        // Camel case must NOT appear.
        #expect(!json.contains("\"externalSessionID\""))
        #expect(!json.contains("\"turnCount\""))
    }
}
