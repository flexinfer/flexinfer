import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("APIClient")
struct APIClientTests {

    @Test("Endpoint paths are correct")
    func endpointPaths() {
        #expect(Endpoint.ping.path == "/api/mobile/v1/ping")
        #expect(Endpoint.dashboard.path == "/api/mobile/v1/dashboard")
        #expect(Endpoint.sessions.path == "/api/mobile/v1/sessions")
        #expect(Endpoint.sessionDetail(id: "s1").path == "/api/mobile/v1/sessions/s1")
        #expect(Endpoint.sessionEvents(id: "s1").path == "/api/mobile/v1/sessions/s1/events")
        #expect(Endpoint.tasks().path == "/api/mobile/v1/tasks")
        #expect(Endpoint.workflows().path == "/api/mobile/v1/workflows")
        #expect(Endpoint.workflowDetail(id: "wf1").path == "/api/mobile/v1/workflows/wf1")
        #expect(Endpoint.presence().path == "/api/mobile/v1/presence")
        #expect(Endpoint.memoryStats.path == "/api/mobile/v1/memory/stats")
        #expect(Endpoint.memoryItems().path == "/api/mobile/v1/memory/items")
        #expect(Endpoint.stream().path == "/api/mobile/v1/stream")
        #expect(Endpoint.topology.path == "/api/mobile/v1/topology")
        #expect(Endpoint.graphStats.path == "/api/mobile/v1/graph/stats")
        #expect(Endpoint.graphEntities().path == "/api/mobile/v1/graph/entities")
        #expect(Endpoint.graphPath(sourceId: "a", targetId: "b").path == "/api/mobile/v1/graph/path")
        #expect(Endpoint.reasoningChains().path == "/api/mobile/v1/reasoning/chains")
        #expect(Endpoint.reasoningChainDetail(id: "chain-1").path == "/api/mobile/v1/reasoning/chains/chain-1")
        #expect(Endpoint.createSession(agentId: "a1").path == "/api/mobile/v1/sessions")
        #expect(Endpoint.endSession(id: "s1").path == "/api/mobile/v1/sessions/s1/end")
        #expect(Endpoint.eventsStream.path == "/api/mobile/v1/events/stream")
    }

    @Test("Endpoint methods are correct")
    func endpointMethods() {
        #expect(Endpoint.ping.method == "GET")
        #expect(Endpoint.dashboard.method == "GET")
        #expect(Endpoint.sessions.method == "GET")
        #expect(Endpoint.tasks().method == "GET")
        #expect(Endpoint.workflows().method == "GET")
        #expect(Endpoint.graphPath(sourceId: "a", targetId: "b").method == "GET")
        #expect(Endpoint.createSession(agentId: "a1").method == "POST")
        #expect(Endpoint.endSession(id: "s1").method == "POST")
    }

    @Test("Mutation endpoints are flagged correctly")
    func mutationFlag() {
        #expect(Endpoint.ping.isMutation == false)
        #expect(Endpoint.dashboard.isMutation == false)
        #expect(Endpoint.stream().isMutation == false)
        #expect(Endpoint.createSession(agentId: "a1").isMutation == true)
        #expect(Endpoint.endSession(id: "s1").isMutation == true)
    }

    @Test("Session events endpoint includes limit query param")
    func sessionEventsLimit() throws {
        let base = URL(string: "https://localhost:3333")!
        let request = try Endpoint.sessionEvents(id: "s1", limit: 50).urlRequest(baseURL: base)
        #expect(request.url?.query?.contains("limit=50") == true)
    }

    @Test("Ops endpoints serialize query parameters")
    func opsQueryParameters() throws {
        let base = URL(string: "https://localhost:3333")!

        let tasksReq = try Endpoint.tasks(status: .inProgress, agentId: "codex", sessionId: "sess-1", limit: 25, search: "mobile").urlRequest(baseURL: base)
        let tasksQuery = tasksReq.url?.query ?? ""
        #expect(tasksQuery.contains("status=in_progress"))
        #expect(tasksQuery.contains("agent_id=codex"))
        #expect(tasksQuery.contains("session_id=sess-1"))
        #expect(tasksQuery.contains("limit=25"))

        let memReq = try Endpoint.memoryItems(tier: .shortTerm, query: "errors", limit: 10).urlRequest(baseURL: base)
        let memQuery = memReq.url?.query ?? ""
        #expect(memQuery.contains("tier=short_term"))
        #expect(memQuery.contains("query=errors"))
        #expect(memQuery.contains("limit=10"))

        let pathReq = try Endpoint.graphPath(sourceId: "ent-a", targetId: "ent-b", maxDepth: 7).urlRequest(baseURL: base)
        let pathQuery = pathReq.url?.query ?? ""
        #expect(pathQuery.contains("source_id=ent-a"))
        #expect(pathQuery.contains("target_id=ent-b"))
        #expect(pathQuery.contains("max_depth=7"))
    }

    @Test("Create session includes body")
    func createSessionBody() throws {
        let base = URL(string: "https://localhost:3333")!
        let request = try Endpoint.createSession(
            agentId: "claude-code",
            namespace: "loom-core/main",
            description: "Test session"
        ).urlRequest(baseURL: base)

        let body = try #require(request.httpBody)
        let json = try JSONSerialization.jsonObject(with: body) as! [String: Any]
        #expect(json["agent_id"] as? String == "claude-code")
        #expect(json["namespace"] as? String == "loom-core/main")
        #expect(json["description"] as? String == "Test session")
    }

    @Test("End session includes body")
    func endSessionBody() throws {
        let base = URL(string: "https://localhost:3333")!
        let request = try Endpoint.endSession(id: "s1", summarize: true).urlRequest(baseURL: base)

        let body = try #require(request.httpBody)
        let json = try JSONSerialization.jsonObject(with: body) as! [String: Any]
        #expect(json["summarize"] as? Bool == true)
        #expect(request.url?.path.hasSuffix("/sessions/s1/end") == true)
    }

    @Test("Create session auto_recall field")
    func createSessionAutoRecall() throws {
        let base = URL(string: "https://localhost:3333")!
        let request = try Endpoint.createSession(
            agentId: "codex",
            autoRecall: true
        ).urlRequest(baseURL: base)

        let body = try #require(request.httpBody)
        let json = try JSONSerialization.jsonObject(with: body) as! [String: Any]
        #expect(json["agent_id"] as? String == "codex")
        #expect(json["auto_recall"] as? Bool == true)
        #expect(json["namespace"] == nil)
    }

    @Test("Error code parsing")
    func errorCodeParsing() {
        #expect(APIErrorCode(rawValue: "unauthorized") == .unauthorized)
        #expect(APIErrorCode(rawValue: "token_revoked") == .tokenRevoked)
        #expect(APIErrorCode(rawValue: "rate_limited") == .rateLimited)
        #expect(APIErrorCode(rawValue: "not_found") == .notFound)
        #expect(APIErrorCode(rawValue: "unknown_code") == nil)
    }

    @Test("Decode contract: successful envelope returns typed payload")
    func decodeContractSuccess() throws {
        let client = makeClient()
        let payload = """
        {
          "ok": true,
          "data": { "pong": true },
          "meta": { "request_id": "req_ok", "timestamp": "2026-02-25T15:00:00Z" }
        }
        """

        let response: TestPong = try client.decodeResponse(Data(payload.utf8), statusCode: 200)
        #expect(response.pong == true)
    }

    @Test("Decode contract: 2xx with missing data throws decoding error")
    func decodeContractMissingData() throws {
        let client = makeClient()
        let payload = """
        {
          "ok": true,
          "meta": { "request_id": "req_missing_data", "timestamp": "2026-02-25T15:00:00Z" }
        }
        """

        do {
            let _: TestPong = try client.decodeResponse(Data(payload.utf8), statusCode: 200)
            Issue.record("Expected decoding error for missing data payload")
        } catch let error as LoomAPIError {
            guard case let .decodingError(underlying) = error else {
                Issue.record("Expected decodingError, got \(error)")
                return
            }
            #expect(underlying.contains("Missing data payload"))
        } catch {
            Issue.record("Expected LoomAPIError, got \(error)")
        }
    }

    @Test("Decode contract: 2xx invalid envelope throws decoding error")
    func decodeContractInvalidEnvelopeOn2xx() throws {
        let client = makeClient()
        let payload = """
        {"not":"an-envelope"}
        """

        do {
            let _: TestPong = try client.decodeResponse(Data(payload.utf8), statusCode: 200)
            Issue.record("Expected decoding error for invalid 2xx envelope")
        } catch let error as LoomAPIError {
            guard case let .decodingError(underlying) = error else {
                Issue.record("Expected decodingError, got \(error)")
                return
            }
            #expect(underlying.contains("Invalid API response contract"))
        } catch {
            Issue.record("Expected LoomAPIError, got \(error)")
        }
    }

    @Test("Decode contract: envelope error maps to apiError")
    func decodeContractEnvelopeError() throws {
        let client = makeClient()
        let payload = """
        {
          "ok": false,
          "error": { "code": "not_found", "message": "session not found" },
          "meta": { "request_id": "req_not_found", "timestamp": "2026-02-25T15:00:00Z" }
        }
        """

        do {
            let _: TestPong = try client.decodeResponse(Data(payload.utf8), statusCode: 404)
            Issue.record("Expected API error for envelope with ok=false")
        } catch let error as LoomAPIError {
            guard case let .apiError(code, message, requestId) = error else {
                Issue.record("Expected apiError, got \(error)")
                return
            }
            #expect(code == .notFound)
            #expect(message == "session not found")
            #expect(requestId == "req_not_found")
        } catch {
            Issue.record("Expected LoomAPIError, got \(error)")
        }
    }

    @Test("Decode contract: non-envelope 404 falls back to notFound API error")
    func decodeContractNonEnvelope404Fallback() throws {
        let client = makeClient()
        let payload = """
        {"message":"plain upstream 404"}
        """

        do {
            let _: TestPong = try client.decodeResponse(Data(payload.utf8), statusCode: 404)
            Issue.record("Expected fallback notFound API error")
        } catch let error as LoomAPIError {
            guard case let .apiError(code, message, _) = error else {
                Issue.record("Expected apiError, got \(error)")
                return
            }
            #expect(code == .notFound)
            #expect(message == "Not found")
        } catch {
            Issue.record("Expected LoomAPIError, got \(error)")
        }
    }

    @Test("Decode contract: non-envelope 503 falls back to upstreamError")
    func decodeContractNonEnvelope503Fallback() throws {
        let client = makeClient()
        let payload = """
        unavailable
        """

        do {
            let _: TestPong = try client.decodeResponse(Data(payload.utf8), statusCode: 503)
            Issue.record("Expected fallback upstreamError API error")
        } catch let error as LoomAPIError {
            guard case let .apiError(code, message, _) = error else {
                Issue.record("Expected apiError, got \(error)")
                return
            }
            #expect(code == .upstreamError)
            #expect(message.contains("HTTP 503"))
        } catch {
            Issue.record("Expected LoomAPIError, got \(error)")
        }
    }
}

private struct TestPong: Decodable {
    let pong: Bool
}

private func makeClient() -> APIClient {
    APIClient(baseURL: URL(string: "https://localhost:3333")!, token: "test-token")
}
