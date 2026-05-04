import Foundation
import Testing
@testable import LoomCompanionKit

/// Phase 7 slice 7.5 — read-only Mills API client coverage.
@Suite("MillsAPI")
struct MillsAPITests {

    @Test("pipelineRuns decodes the operator's uppercased payload")
    func decodesPipelineRuns() async throws {
        let mock = MockAPIClient()
        mock.millsPipelineRunsResponse = [
            MillsPipelineRun(id: "PIPE-A", backlogID: "BACK-A", template: "mills-default",
                            state: "implementing", attempts: 1, depth: 0),
            MillsPipelineRun(id: "PIPE-A-S1", backlogID: "BACK-CHILD", template: "mills-default",
                            state: "testing", attempts: 0, parentRunID: "PIPE-A", depth: 1),
        ]
        let api = MillsAPI(client: mock)
        let runs = try await api.pipelineRuns()
        #expect(runs.count == 2)
        #expect(runs[0].id == "PIPE-A")
        #expect(runs[0].depth == 0)
        #expect(runs[1].parentRunID == "PIPE-A")
        #expect(runs[1].depth == 1)
    }

    @Test("latestKPI returns the snapshot when present")
    func latestKPISuccess() async throws {
        let mock = MockAPIClient()
        mock.millsKPIResponse = MillsKPISnapshot(metrics: [
            "pipeline_merge_rate": 0.93,
            "council_cost_per_day_usd": 4.22,
        ])
        let api = MillsAPI(client: mock)
        let snap = try await api.latestKPI(window: "1d")
        #expect(snap != nil)
        #expect(snap?.metrics["pipeline_merge_rate"] == 0.93)
    }

    @Test("latestKPI returns nil on 404 (no snapshot yet)")
    func latestKPISwallows404() async throws {
        let mock = MockAPIClient()
        mock.endpointFailures["/api/mills/kpis"] = .apiError(code: .notFound, message: "Not found", requestId: "")
        let api = MillsAPI(client: mock)
        let snap = try await api.latestKPI(window: "1d")
        #expect(snap == nil)
    }

    @Test("latestKPI returns nil when operator URL not configured (HUD 503)")
    func latestKPISwallowsNotConfigured() async throws {
        let mock = MockAPIClient()
        mock.endpointFailures["/api/mills/kpis"] = .apiError(code: .notConfigured, message: "operator unset", requestId: "")
        let api = MillsAPI(client: mock)
        let snap = try await api.latestKPI(window: "1d")
        #expect(snap == nil)
    }

    @Test("PipelineRun JSON decoder handles operator-shaped payload")
    func decodesPipelineRunFromOperatorJSON() throws {
        let json = """
        {
          "ID": "PIPE-CHILD",
          "BacklogID": "BACK-Q",
          "Template": "mills-default",
          "State": "implementing",
          "Attempts": 1,
          "ParentRunID": "PIPE-PARENT",
          "Depth": 1
        }
        """.data(using: .utf8)!
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let run = try decoder.decode(MillsPipelineRun.self, from: json)
        #expect(run.id == "PIPE-CHILD")
        #expect(run.parentRunID == "PIPE-PARENT")
        #expect(run.depth == 1)
    }
}
