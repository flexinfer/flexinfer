import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("PushRegistration DTOs")
struct PushRegistrationTests {

    // MARK: - PushRegistrationRequest

    @Test("PushRegistrationRequest encodes to expected JSON")
    func requestEncoding() throws {
        let req = PushRegistrationRequest(token: "abc123", platform: .apns)
        let data = try JSONEncoder().encode(req)
        let dict = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        #expect(dict["token"] as? String == "abc123")
        #expect(dict["platform"] as? String == "apns")
    }

    @Test("PushRegistrationRequest round-trips through JSON")
    func requestRoundTrip() throws {
        let original = PushRegistrationRequest(token: "device-tok-xyz", platform: .fcm)
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PushRegistrationRequest.self, from: data)
        #expect(decoded.token == original.token)
        #expect(decoded.platform == original.platform)
    }

    @Test("PushRegistrationRequest decodes from server-like JSON")
    func requestDecodeFromJSON() throws {
        let json = "{\"token\":\"tok-99\",\"platform\":\"apns\"}"
        let data = json.data(using: .utf8)!
        let req = try JSONDecoder().decode(PushRegistrationRequest.self, from: data)
        #expect(req.token == "tok-99")
        #expect(req.platform == .apns)
    }

    // MARK: - PushPlatform

    @Test("PushPlatform raw values match API contract")
    func platformRawValues() {
        #expect(PushPlatform.apns.rawValue == "apns")
        #expect(PushPlatform.fcm.rawValue == "fcm")
    }

    @Test("PushPlatform decodes from raw string")
    func platformDecode() throws {
        let json = "\"fcm\""
        let data = json.data(using: .utf8)!
        let platform = try JSONDecoder().decode(PushPlatform.self, from: data)
        #expect(platform == .fcm)
    }

    @Test("PushPlatform rejects unknown platform")
    func platformRejectsUnknown() {
        let json = "\"webpush\""
        let data = json.data(using: .utf8)!
        #expect(throws: DecodingError.self) {
            _ = try JSONDecoder().decode(PushPlatform.self, from: data)
        }
    }

    // MARK: - PushRegistrationResponse

    @Test("PushRegistrationResponse decodes snake_case registration_id")
    func responseDecodeSnakeCase() throws {
        let json = "{\"registered\":true,\"registration_id\":\"reg_abc12345\"}"
        let data = json.data(using: .utf8)!
        let resp = try JSONDecoder().decode(PushRegistrationResponse.self, from: data)
        #expect(resp.registered == true)
        #expect(resp.registrationId == "reg_abc12345")
    }

    @Test("PushRegistrationResponse round-trips correctly")
    func responseRoundTrip() throws {
        let original = PushRegistrationResponse(registered: true, registrationId: "reg_ff00")
        let encoder = JSONEncoder()
        let data = try encoder.encode(original)
        let decoded = try JSONDecoder().decode(PushRegistrationResponse.self, from: data)
        #expect(decoded.registered == original.registered)
        #expect(decoded.registrationId == original.registrationId)
    }

    @Test("PushRegistrationResponse encodes registration_id as snake_case")
    func responseEncodesSnakeCase() throws {
        let resp = PushRegistrationResponse(registered: true, registrationId: "reg_test")
        let data = try JSONEncoder().encode(resp)
        let dict = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        #expect(dict["registration_id"] as? String == "reg_test")
        #expect(dict["registrationId"] == nil)
    }

    // MARK: - PushUnregisterRequest

    @Test("PushUnregisterRequest encodes token")
    func unregisterRequestEncoding() throws {
        let req = PushUnregisterRequest(token: "old-token")
        let data = try JSONEncoder().encode(req)
        let dict = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        #expect(dict["token"] as? String == "old-token")
    }

    @Test("PushUnregisterRequest round-trips")
    func unregisterRequestRoundTrip() throws {
        let original = PushUnregisterRequest(token: "remove-me")
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(PushUnregisterRequest.self, from: data)
        #expect(decoded.token == original.token)
    }

    // MARK: - PushUnregisterResponse

    @Test("PushUnregisterResponse decodes removed flag")
    func unregisterResponseDecode() throws {
        let json = "{\"removed\":true}"
        let data = json.data(using: .utf8)!
        let resp = try JSONDecoder().decode(PushUnregisterResponse.self, from: data)
        #expect(resp.removed == true)
    }

    @Test("PushUnregisterResponse decodes false removed")
    func unregisterResponseFalse() throws {
        let json = "{\"removed\":false}"
        let data = json.data(using: .utf8)!
        let resp = try JSONDecoder().decode(PushUnregisterResponse.self, from: data)
        #expect(resp.removed == false)
    }

    // MARK: - Sendable conformance

    @Test("All push DTOs are Sendable")
    func sendableConformance() {
        // Compile-time check: these closures prove Sendable conformance.
        let _: @Sendable () -> PushRegistrationRequest = { PushRegistrationRequest(token: "t", platform: .apns) }
        let _: @Sendable () -> PushRegistrationResponse = { PushRegistrationResponse(registered: true, registrationId: "r") }
        let _: @Sendable () -> PushUnregisterRequest = { PushUnregisterRequest(token: "t") }
        let _: @Sendable () -> PushUnregisterResponse = { PushUnregisterResponse(removed: true) }
        // If this compiles, Sendable is satisfied.
    }
}
