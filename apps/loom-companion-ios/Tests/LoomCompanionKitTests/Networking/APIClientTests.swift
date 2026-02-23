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
        #expect(Endpoint.createSession(agentId: "a1").path == "/api/mobile/v1/sessions")
        #expect(Endpoint.endSession(id: "s1").path == "/api/mobile/v1/sessions/s1/end")
        #expect(Endpoint.eventsStream.path == "/api/mobile/v1/events/stream")
    }

    @Test("Endpoint methods are correct")
    func endpointMethods() {
        #expect(Endpoint.ping.method == "GET")
        #expect(Endpoint.dashboard.method == "GET")
        #expect(Endpoint.sessions.method == "GET")
        #expect(Endpoint.createSession(agentId: "a1").method == "POST")
        #expect(Endpoint.endSession(id: "s1").method == "POST")
    }

    @Test("Mutation endpoints are flagged correctly")
    func mutationFlag() {
        #expect(Endpoint.ping.isMutation == false)
        #expect(Endpoint.dashboard.isMutation == false)
        #expect(Endpoint.createSession(agentId: "a1").isMutation == true)
        #expect(Endpoint.endSession(id: "s1").isMutation == true)
    }

    @Test("Session events endpoint includes limit query param")
    func sessionEventsLimit() throws {
        let base = URL(string: "https://localhost:3333")!
        let request = try Endpoint.sessionEvents(id: "s1", limit: 50).urlRequest(baseURL: base)
        #expect(request.url?.query?.contains("limit=50") == true)
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

    @Test("Error code parsing")
    func errorCodeParsing() {
        #expect(APIErrorCode(rawValue: "unauthorized") == .unauthorized)
        #expect(APIErrorCode(rawValue: "token_revoked") == .tokenRevoked)
        #expect(APIErrorCode(rawValue: "rate_limited") == .rateLimited)
        #expect(APIErrorCode(rawValue: "not_found") == .notFound)
        #expect(APIErrorCode(rawValue: "unknown_code") == nil)
    }
}
