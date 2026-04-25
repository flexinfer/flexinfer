import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("SpawnBudgetWidgetData")
struct SpawnBudgetWidgetDataTests {

    // MARK: - Codable round-trip

    @Test("Encoded keys use snake_case wire shape")
    func encodesSnakeCaseKeys() throws {
        let entry = SpawnBudgetWidgetEntry(
            spawnId: "spawn_x",
            agentType: "claude-code",
            namespace: "loom-core/feature",
            status: "running",
            totalCostUSD: 0.42,
            costEstimated: true,
            maxCostUSD: 1.0,
            turnCount: 6,
            maxTurns: 20,
            startedAt: "2026-04-24T10:00:00Z"
        )
        let snapshot = SpawnBudgetWidgetData(entries: [entry])
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let data = try encoder.encode(snapshot)
        let json = try #require(String(data: data, encoding: .utf8))

        #expect(json.contains("\"spawn_id\":\"spawn_x\""))
        #expect(json.contains("\"agent_type\":\"claude-code\""))
        #expect(json.contains("\"total_cost_usd\":0.42"))
        #expect(json.contains("\"cost_estimated\":true"))
        #expect(json.contains("\"max_cost_usd\":1"))
        #expect(json.contains("\"turn_count\":6"))
        #expect(json.contains("\"max_turns\":20"))
        // Camel case must NOT appear at the wire level.
        #expect(!json.contains("\"spawnId\""))
        #expect(!json.contains("\"agentType\""))
        #expect(!json.contains("\"totalCostUSD\""))
    }

    @Test("Round-trip decode preserves equality")
    func roundTripPreservesEquality() throws {
        let original = SpawnBudgetWidgetData(entries: [
            SpawnBudgetWidgetEntry(
                spawnId: "a",
                agentType: "claude-code",
                namespace: "ns/a",
                status: "running",
                totalCostUSD: 0.1,
                costEstimated: false,
                maxCostUSD: 0.5,
                turnCount: 1,
                maxTurns: 10,
                startedAt: "t"
            ),
            SpawnBudgetWidgetEntry(
                spawnId: "b",
                agentType: "codex",
                namespace: "ns/b",
                status: "running",
                totalCostUSD: 0,
                costEstimated: true,
                maxCostUSD: nil,
                turnCount: 0,
                maxTurns: nil,
                startedAt: "t"
            ),
        ])
        let encoded = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(SpawnBudgetWidgetData.self, from: encoded)
        #expect(decoded.entries == original.entries)
    }

    // MARK: - Budget fractions

    @Test("costFraction returns nil without cap, clamps to 1.0 above cap")
    func costFractionBehavior() {
        let noCap = makeEntry(totalCostUSD: 5.0, maxCostUSD: nil)
        #expect(noCap.costFraction == nil)

        let zeroCap = makeEntry(totalCostUSD: 5.0, maxCostUSD: 0)
        #expect(zeroCap.costFraction == nil)

        let underCap = makeEntry(totalCostUSD: 0.3, maxCostUSD: 1.0)
        #expect(underCap.costFraction == 0.3)

        let overCap = makeEntry(totalCostUSD: 2.0, maxCostUSD: 1.0)
        #expect(overCap.costFraction == 1.0)
    }

    @Test("turnFraction returns nil without cap, clamps to 1.0 above cap")
    func turnFractionBehavior() {
        let noCap = makeEntry(turnCount: 50, maxTurns: nil)
        #expect(noCap.turnFraction == nil)

        let underCap = makeEntry(turnCount: 5, maxTurns: 10)
        #expect(underCap.turnFraction == 0.5)

        let overCap = makeEntry(turnCount: 15, maxTurns: 10)
        #expect(overCap.turnFraction == 1.0)
    }

    // MARK: - from(spawns:telemetry:)

    @Test("Builder filters non-active spawns")
    func builderFiltersNonActive() {
        let spawns: [MobileSpawnStatus] = [
            makeSpawn(id: "active1", status: "running"),
            makeSpawn(id: "done1", status: "completed"),
            makeSpawn(id: "failed1", status: "failed"),
            makeSpawn(id: "creating1", status: "creating"),
        ]
        let snapshot = SpawnBudgetWidgetData.from(spawns: spawns, telemetry: [:])
        let ids = snapshot.entries.map(\.spawnId).sorted()
        #expect(ids == ["active1", "creating1"])
    }

    @Test("Builder orders by total cost desc, ties broken by turns then id")
    func builderRanksByCost() {
        let telemetry: [String: SpawnTelemetry] = [
            "low": SpawnTelemetry(turnCount: 1, totalCostUSD: 0.01),
            "high": SpawnTelemetry(turnCount: 5, totalCostUSD: 1.5),
            "mid_a": SpawnTelemetry(turnCount: 10, totalCostUSD: 0.5),
            "mid_b": SpawnTelemetry(turnCount: 20, totalCostUSD: 0.5),
        ]
        let spawns = ["low", "high", "mid_a", "mid_b"].map { makeSpawn(id: $0, status: "running") }
        let snapshot = SpawnBudgetWidgetData.from(spawns: spawns, telemetry: telemetry)
        let ids = snapshot.entries.map(\.spawnId)
        // high (1.5) > mid_b (0.5, turns=20) > mid_a (0.5, turns=10) > low (0.01)
        #expect(ids == ["high", "mid_b", "mid_a", "low"])
    }

    @Test("Builder caps at limit")
    func builderRespectsLimit() {
        let spawns = (0..<10).map { makeSpawn(id: "s\($0)", status: "running") }
        let snapshot = SpawnBudgetWidgetData.from(spawns: spawns, telemetry: [:], limit: 3)
        #expect(snapshot.entries.count == 3)
    }

    @Test("Builder propagates cost_estimated and turn count from telemetry")
    func builderPropagatesTelemetry() {
        let telemetry: [String: SpawnTelemetry] = [
            "codex_spawn": SpawnTelemetry(turnCount: 3, totalCostUSD: 0.05, costEstimated: true),
            "claude_spawn": SpawnTelemetry(turnCount: 2, totalCostUSD: 0.5, costEstimated: false),
        ]
        let spawns = [
            makeSpawn(id: "codex_spawn", agentType: "codex", status: "running"),
            makeSpawn(id: "claude_spawn", agentType: "claude-code", status: "running"),
        ]
        let snapshot = SpawnBudgetWidgetData.from(spawns: spawns, telemetry: telemetry)
        let codex = try! #require(snapshot.entries.first { $0.spawnId == "codex_spawn" })
        #expect(codex.costEstimated == true)
        #expect(codex.turnCount == 3)
        let claude = try! #require(snapshot.entries.first { $0.spawnId == "claude_spawn" })
        #expect(claude.costEstimated == false)
        #expect(claude.turnCount == 2)
    }

    @Test("Builder zero-fills entries with no telemetry")
    func builderZeroFillsMissingTelemetry() {
        let spawns = [makeSpawn(id: "fresh", status: "running")]
        let snapshot = SpawnBudgetWidgetData.from(spawns: spawns, telemetry: [:])
        let entry = try! #require(snapshot.entries.first)
        #expect(entry.totalCostUSD == 0)
        #expect(entry.turnCount == 0)
        #expect(entry.costEstimated == false)
    }

    @Test("Builder uses request.namespace then falls back to project")
    func builderNamespaceFallback() {
        let withNs = makeSpawn(id: "withNs", status: "running", namespace: "loom-core/feat")
        let withoutNs = makeSpawn(id: "withoutNs", status: "running", namespace: nil, project: "loom-core")
        let snapshot = SpawnBudgetWidgetData.from(spawns: [withNs, withoutNs], telemetry: [:])
        let nsEntry = try! #require(snapshot.entries.first { $0.spawnId == "withNs" })
        #expect(nsEntry.namespace == "loom-core/feat")
        let projEntry = try! #require(snapshot.entries.first { $0.spawnId == "withoutNs" })
        #expect(projEntry.namespace == "loom-core")
    }

    @Test("Builder pulls maxCostUSD/maxTurns from spawn request")
    func builderPullsBudgetCaps() {
        let spawn = makeSpawn(id: "capped", status: "running", maxCostUSD: 2.5, maxTurns: 15)
        let snapshot = SpawnBudgetWidgetData.from(spawns: [spawn], telemetry: [:])
        let entry = try! #require(snapshot.entries.first)
        #expect(entry.maxCostUSD == 2.5)
        #expect(entry.maxTurns == 15)
    }

    @Test("Builder yields empty snapshot when no active spawns")
    func builderEmptyWhenNoneActive() {
        let snapshot = SpawnBudgetWidgetData.from(
            spawns: [makeSpawn(id: "done", status: "completed")],
            telemetry: [:]
        )
        #expect(snapshot.entries.isEmpty)
    }

    // MARK: - Helpers

    private func makeEntry(
        totalCostUSD: Double = 0,
        maxCostUSD: Double? = nil,
        turnCount: Int = 0,
        maxTurns: Int? = nil
    ) -> SpawnBudgetWidgetEntry {
        SpawnBudgetWidgetEntry(
            spawnId: "x",
            agentType: "claude-code",
            namespace: "ns",
            status: "running",
            totalCostUSD: totalCostUSD,
            maxCostUSD: maxCostUSD,
            turnCount: turnCount,
            maxTurns: maxTurns,
            startedAt: "t"
        )
    }

    private func makeSpawn(
        id: String,
        agentType: String = "claude-code",
        status: String = "running",
        namespace: String? = "ns",
        project: String = "loom-core",
        maxCostUSD: Double? = nil,
        maxTurns: Int? = nil
    ) -> MobileSpawnStatus {
        // MobileSpawnStatus has no public memberwise init; round-trip via JSON.
        // Build the request dict by appending only non-nil optionals to avoid
        // JSONSerialization's "invalid value (Optional)" trap.
        var requestDict: [String: Any] = [
            "agent_type": agentType,
            "project": project,
            "task_description": "test",
        ]
        if let namespace { requestDict["namespace"] = namespace }
        if let maxCostUSD { requestDict["max_cost_usd"] = maxCostUSD }
        if let maxTurns { requestDict["max_turns"] = maxTurns }

        let json: [String: Any] = [
            "spawn_id": id,
            "agent_id": "agent-\(id)",
            "status": status,
            "started_at": "2026-04-24T10:00:00Z",
            "request": requestDict,
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        return try! JSONDecoder().decode(MobileSpawnStatus.self, from: data)
    }
}
