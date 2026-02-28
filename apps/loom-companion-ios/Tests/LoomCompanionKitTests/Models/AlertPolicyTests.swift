import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("AlertPolicy DTOs")
struct AlertPolicyTests {

    @Test("Decodes policy response with snake_case fields")
    func decodesPolicyResponse() throws {
        let json = """
        {
          "policy": [
            {
              "event_type": "hud.health",
              "severity": "critical",
              "interruption_level": "time_sensitive",
              "title": "Server Down",
              "allowed_actions": ["view_dashboard", "acknowledge"],
              "conditional": true
            }
          ],
          "version": "v1"
        }
        """
        let data = json.data(using: .utf8)!
        let response = try JSONDecoder().decode(MobileAlertPolicyResponse.self, from: data)

        #expect(response.version == "v1")
        #expect(response.policy.count == 1)
        #expect(response.policy[0].eventType == "hud.health")
        #expect(response.policy[0].interruptionLevel == "time_sensitive")
        #expect(response.policy[0].allowedActions == ["view_dashboard", "acknowledge"])
    }
}
