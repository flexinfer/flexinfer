import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("APIEnvelope Decoding")
struct APIEnvelopeTests {

    @Test("Decodes success envelope with data")
    func decodesSuccessEnvelope() throws {
        let json = """
        {"ok":true,"data":{"pong":true},"meta":{"request_id":"req_001","timestamp":"2026-02-23T12:00:00Z"}}
        """
        let envelope = try JSONDecoder().decode(APIEnvelope<PongData>.self, from: Data(json.utf8))
        #expect(envelope.ok == true)
        #expect(envelope.data?.pong == true)
        #expect(envelope.error == nil)
        #expect(envelope.meta.requestId == "req_001")
        #expect(envelope.meta.timestamp == "2026-02-23T12:00:00Z")
    }

    @Test("Decodes error envelope")
    func decodesErrorEnvelope() throws {
        let data = try loadFixture("error_unauthorized")
        let envelope = try JSONDecoder().decode(APIEnvelope<PongData>.self, from: data)
        #expect(envelope.ok == false)
        #expect(envelope.data == nil)
        #expect(envelope.error?.code == "unauthorized")
        #expect(envelope.error?.message == "invalid or missing bearer token")
        #expect(envelope.meta.requestId == "req_err001")
    }

    @Test("Decodes rate limited error")
    func decodesRateLimitedError() throws {
        let data = try loadFixture("error_rate_limited")
        let envelope = try JSONDecoder().decode(APIEnvelope<PongData>.self, from: data)
        #expect(envelope.ok == false)
        #expect(envelope.error?.code == "rate_limited")
    }
}

private struct PongData: Decodable {
    let pong: Bool
}
