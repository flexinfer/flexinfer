import Testing
import Foundation
@testable import LoomCompanionKit

/// Unit tests for the Wave 2 spawn telemetry and multi-turn control plane
/// endpoint cases added in slice 14b. These exercise the URL, method, query
/// string, mutation flag, and POST body encoding for each new case without
/// touching the network layer.
@Suite("SpawnTelemetryEndpoints")
struct SpawnTelemetryEndpointTests {
    private let base = URL(string: "https://localhost:3333")!

    // MARK: - Paths

    @Test("Spawn telemetry endpoint paths")
    func telemetryPaths() {
        #expect(
            Endpoint.spawnTelemetry(id: "abc").path
                == "/api/mobile/v1/agent/spawn/abc/telemetry"
        )
        #expect(
            Endpoint.spawnTelemetryTools(id: "abc").path
                == "/api/mobile/v1/agent/spawn/abc/telemetry/tools"
        )
        #expect(
            Endpoint.spawnTelemetryFiles(id: "abc").path
                == "/api/mobile/v1/agent/spawn/abc/telemetry/files"
        )
        #expect(
            Endpoint.spawnTelemetryErrors(id: "abc").path
                == "/api/mobile/v1/agent/spawn/abc/telemetry/errors"
        )
        #expect(
            Endpoint.spawnSendMessage(id: "abc", text: "hi").path
                == "/api/mobile/v1/agent/spawn/abc/message"
        )
        #expect(
            Endpoint.spawnInterrupt(id: "abc").path
                == "/api/mobile/v1/agent/spawn/abc/interrupt"
        )
    }

    // MARK: - Methods

    @Test("Spawn telemetry endpoint methods")
    func telemetryMethods() {
        #expect(Endpoint.spawnTelemetry(id: "abc").method == "GET")
        #expect(Endpoint.spawnTelemetryTools(id: "abc").method == "GET")
        #expect(Endpoint.spawnTelemetryFiles(id: "abc").method == "GET")
        #expect(Endpoint.spawnTelemetryErrors(id: "abc").method == "GET")
        #expect(Endpoint.spawnSendMessage(id: "abc", text: "hi").method == "POST")
        #expect(Endpoint.spawnInterrupt(id: "abc").method == "POST")
    }

    // MARK: - Mutation flag

    @Test("Spawn telemetry GET endpoints are non-mutating")
    func telemetryGetEndpointsAreNonMutating() {
        #expect(Endpoint.spawnTelemetry(id: "abc").isMutation == false)
        #expect(Endpoint.spawnTelemetryTools(id: "abc").isMutation == false)
        #expect(Endpoint.spawnTelemetryFiles(id: "abc").isMutation == false)
        #expect(Endpoint.spawnTelemetryErrors(id: "abc").isMutation == false)
    }

    @Test("Spawn control POST endpoints are flagged as mutations")
    func controlEndpointsAreMutations() {
        #expect(Endpoint.spawnSendMessage(id: "abc", text: "hi").isMutation == true)
        #expect(Endpoint.spawnInterrupt(id: "abc").isMutation == true)
    }

    // MARK: - Query parameters

    @Test("Spawn telemetry pagination has no query params by default")
    func telemetryNoQueryByDefault() throws {
        let request = try Endpoint.spawnTelemetryTools(id: "abc").urlRequest(baseURL: base)
        #expect(request.url?.query == nil)
    }

    @Test("Spawn telemetry tools endpoint serializes offset/limit")
    func telemetryToolsQueryParams() throws {
        let request = try Endpoint.spawnTelemetryTools(
            id: "abc",
            offset: 100,
            limit: 25
        ).urlRequest(baseURL: base)
        let query = request.url?.query ?? ""
        #expect(query.contains("offset=100"))
        #expect(query.contains("limit=25"))
    }

    @Test("Spawn telemetry files endpoint serializes offset/limit")
    func telemetryFilesQueryParams() throws {
        let request = try Endpoint.spawnTelemetryFiles(
            id: "abc",
            offset: 0,
            limit: 50
        ).urlRequest(baseURL: base)
        let query = request.url?.query ?? ""
        #expect(query.contains("offset=0"))
        #expect(query.contains("limit=50"))
    }

    @Test("Spawn telemetry errors endpoint serializes offset/limit")
    func telemetryErrorsQueryParams() throws {
        let request = try Endpoint.spawnTelemetryErrors(
            id: "abc",
            offset: 10,
            limit: 5
        ).urlRequest(baseURL: base)
        let query = request.url?.query ?? ""
        #expect(query.contains("offset=10"))
        #expect(query.contains("limit=5"))
    }

    @Test("Spawn telemetry tools endpoint allows partial query params")
    func telemetryToolsPartialQueryParams() throws {
        let limitOnly = try Endpoint.spawnTelemetryTools(
            id: "abc",
            limit: 42
        ).urlRequest(baseURL: base)
        let limitQuery = limitOnly.url?.query ?? ""
        #expect(limitQuery.contains("limit=42"))
        #expect(!limitQuery.contains("offset"))

        let offsetOnly = try Endpoint.spawnTelemetryTools(
            id: "abc",
            offset: 7
        ).urlRequest(baseURL: base)
        let offsetQuery = offsetOnly.url?.query ?? ""
        #expect(offsetQuery.contains("offset=7"))
        #expect(!offsetQuery.contains("limit"))
    }

    // MARK: - Request bodies

    @Test("Spawn send message endpoint includes text body")
    func sendMessageBody() throws {
        let request = try Endpoint.spawnSendMessage(
            id: "abc",
            text: "please try again"
        ).urlRequest(baseURL: base)

        #expect(request.value(forHTTPHeaderField: "Content-Type") == "application/json")
        let body = try #require(request.httpBody)
        let json = try JSONSerialization.jsonObject(with: body) as! [String: Any]
        #expect(json["text"] as? String == "please try again")
        #expect(request.url?.path.hasSuffix("/abc/message") == true)
    }

    @Test("Spawn interrupt endpoint includes empty JSON body")
    func interruptBody() throws {
        let request = try Endpoint.spawnInterrupt(id: "abc").urlRequest(baseURL: base)

        #expect(request.value(forHTTPHeaderField: "Content-Type") == "application/json")
        let body = try #require(request.httpBody)
        let json = try JSONSerialization.jsonObject(with: body) as! [String: Any]
        #expect(json.isEmpty)
        #expect(request.url?.path.hasSuffix("/abc/interrupt") == true)
    }

    // MARK: - Path identifier handling

    @Test("Spawn telemetry paths respect custom spawn ids")
    func customSpawnIds() {
        #expect(
            Endpoint.spawnTelemetry(id: "spawn-123").path
                == "/api/mobile/v1/agent/spawn/spawn-123/telemetry"
        )
        #expect(
            Endpoint.spawnTelemetryTools(id: "spawn-999").path
                == "/api/mobile/v1/agent/spawn/spawn-999/telemetry/tools"
        )
    }
}
