import Testing
@testable import LoomCompanionKit

@Suite("ConnectionRemediation")
struct ConnectionRemediationTests {

    // MARK: - Severity mapping

    @Test("Healthy state has ok severity")
    func healthySeverity() {
        let r = ConnectionRemediation.forHealth(.healthy)
        #expect(r.severity == .ok)
        #expect(r.title == "Connected")
        #expect(r.steps.isEmpty)
    }

    @Test("Degraded stream has warning severity")
    func degradedStreamSeverity() {
        let r = ConnectionRemediation.forHealth(.degradedStream)
        #expect(r.severity == .warning)
        #expect(r.title == "Stream Degraded")
        #expect(!r.steps.isEmpty)
    }

    @Test("Auth failure has error severity")
    func authFailureSeverity() {
        let r = ConnectionRemediation.forHealth(.authFailure(message: "bad token"))
        #expect(r.severity == .error)
        #expect(r.title == "Authentication Failed")
        #expect(r.description == "bad token")
    }

    @Test("Auth failure uses default description when message is empty")
    func authFailureEmptyMessage() {
        let r = ConnectionRemediation.forHealth(.authFailure(message: ""))
        #expect(r.description == "The server rejected the bearer token.")
    }

    @Test("Permission denied has error severity")
    func permissionDeniedSeverity() {
        let r = ConnectionRemediation.forHealth(.permissionDenied(message: "missing scope"))
        #expect(r.severity == .error)
        #expect(r.title == "Permission Denied")
        #expect(r.description == "missing scope")
    }

    @Test("Permission denied uses default description when message is empty")
    func permissionDeniedEmptyMessage() {
        let r = ConnectionRemediation.forHealth(.permissionDenied(message: ""))
        #expect(r.description == "The token lacks a required scope.")
    }

    @Test("Gateway route missing has explicit remediation")
    func gatewayRouteMissingSeverity() {
        let r = ConnectionRemediation.forHealth(.gatewayRouteMissing(message: "route missing"))
        #expect(r.severity == .error)
        #expect(r.title == "Gateway Route Missing")
        #expect(r.steps.joined(separator: " ").contains("/api/mobile/v1"))
    }

    @Test("Unreachable has error severity")
    func unreachableSeverity() {
        let r = ConnectionRemediation.forHealth(.unreachable)
        #expect(r.severity == .error)
        #expect(r.title == "Server Unreachable")
        #expect(!r.steps.isEmpty)
    }

    @Test("Rate limited has warning severity")
    func rateLimitedSeverity() {
        let r = ConnectionRemediation.forHealth(.rateLimited)
        #expect(r.severity == .warning)
        #expect(r.title == "Rate Limited")
        #expect(!r.steps.isEmpty)
    }

    @Test("Unknown has warning severity")
    func unknownSeverity() {
        let r = ConnectionRemediation.forHealth(.unknown)
        #expect(r.severity == .warning)
        #expect(r.title == "Not Tested")
    }

    // MARK: - LAN mode steps

    @Test("Degraded stream includes LAN-specific steps in LAN mode")
    func degradedStreamLAN() {
        let r = ConnectionRemediation.forHealth(.degradedStream, mode: .lan)
        let joined = r.steps.joined(separator: " ")
        #expect(joined.contains("same local network"))
        #expect(joined.contains("Local Network permission"))
    }

    @Test("Unreachable includes LAN-specific steps in LAN mode")
    func unreachableLAN() {
        let r = ConnectionRemediation.forHealth(.unreachable, mode: .lan)
        let joined = r.steps.joined(separator: " ")
        #expect(joined.contains("same local network"))
        #expect(joined.contains("Local Network"))
        #expect(joined.contains("firewall"))
    }

    @Test("Auth failure includes LAN-specific steps: none (same steps)")
    func authFailureLAN() {
        let withMode = ConnectionRemediation.forHealth(.authFailure(message: "x"), mode: .lan)
        let without = ConnectionRemediation.forHealth(.authFailure(message: "x"))
        // LAN mode does not change auth failure steps (no gateway header concern)
        #expect(withMode.steps == without.steps)
    }

    // MARK: - Gateway mode steps

    @Test("Degraded stream includes gateway-specific steps in gateway mode")
    func degradedStreamGateway() {
        let r = ConnectionRemediation.forHealth(.degradedStream, mode: .gateway)
        let joined = r.steps.joined(separator: " ")
        #expect(joined.contains("gateway proxy"))
    }

    @Test("Unreachable includes gateway-specific steps in gateway mode")
    func unreachableGateway() {
        let r = ConnectionRemediation.forHealth(.unreachable, mode: .gateway)
        let joined = r.steps.joined(separator: " ")
        #expect(joined.contains("gateway"))
        #expect(joined.contains("TLS"))
    }

    @Test("Auth failure includes gateway-specific step about Authorization header")
    func authFailureGateway() {
        let r = ConnectionRemediation.forHealth(.authFailure(message: "x"), mode: .gateway)
        let joined = r.steps.joined(separator: " ")
        #expect(joined.contains("Authorization header"))
    }

    // MARK: - No mode (nil) gives generic steps

    @Test("Unreachable without mode gives generic network steps")
    func unreachableNoMode() {
        let r = ConnectionRemediation.forHealth(.unreachable)
        let joined = r.steps.joined(separator: " ")
        #expect(joined.contains("network connection"))
        // Should not contain LAN or gateway specific guidance
        #expect(!joined.contains("Local Network"))
        #expect(!joined.contains("gateway"))
    }

    @Test("Degraded stream without mode omits LAN and gateway steps")
    func degradedStreamNoMode() {
        let r = ConnectionRemediation.forHealth(.degradedStream)
        let joined = r.steps.joined(separator: " ")
        #expect(!joined.contains("same local network"))
        #expect(!joined.contains("gateway proxy"))
    }

    // MARK: - Equatable conformance

    @Test("Same health and mode produce equal remediations")
    func equatable() {
        let a = ConnectionRemediation.forHealth(.healthy)
        let b = ConnectionRemediation.forHealth(.healthy)
        #expect(a == b)
    }

    @Test("Different health states produce different remediations")
    func notEqual() {
        let a = ConnectionRemediation.forHealth(.healthy)
        let b = ConnectionRemediation.forHealth(.unreachable)
        #expect(a != b)
    }
}
