import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("ConnectionHealthMonitor")
struct ConnectionHealthTests {

    @Test("Initial state is unknown")
    func initialStateUnknown() {
        let monitor = ConnectionHealthMonitor()
        #expect(monitor.health == .unknown)
        #expect(monitor.isPollingFallback == false)
    }

    @Test("Transitions to healthy on success from unknown")
    func unknownToHealthy() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess()
        #expect(monitor.health == .healthy)
    }

    @Test("Transitions to authFailure on 401")
    func unknownToAuthFailure() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleAPIError(.apiError(code: .unauthorized, message: "bad token", requestId: "r1"))
        #expect(monitor.health == .authFailure(message: "bad token"))
    }

    @Test("Transitions to authFailure on token_revoked")
    func healthyToTokenRevoked() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess()
        #expect(monitor.health == .healthy)

        monitor.handleAPIError(.apiError(code: .tokenRevoked, message: "revoked", requestId: "r2"))
        #expect(monitor.health == .authFailure(message: "revoked"))
    }

    @Test("Transitions to unreachable on network error")
    func healthyToUnreachable() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess()
        monitor.handleAPIError(.networkError(underlying: "connection refused"))
        #expect(monitor.health == .unreachable)
    }

    @Test("Transitions to degradedStream on SSE disconnect")
    func healthyToDegradedStream() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess()
        #expect(monitor.health == .healthy)

        monitor.handleSSEStateChange(.disconnected)
        #expect(monitor.health == .degradedStream)
        #expect(monitor.isPollingFallback == true)
    }

    @Test("Recovers from degradedStream on SSE reconnect")
    func degradedStreamToHealthy() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess()
        monitor.handleSSEStateChange(.disconnected)
        #expect(monitor.health == .degradedStream)

        monitor.handleSSEStateChange(.connected)
        #expect(monitor.health == .healthy)
        #expect(monitor.isPollingFallback == false)
    }

    @Test("Records ping time")
    func recordsPing() {
        let monitor = ConnectionHealthMonitor()
        #expect(monitor.lastPingTime == nil)
        monitor.recordPing()
        #expect(monitor.lastPingTime != nil)
        #expect(monitor.health == .healthy)
    }

    @Test("Rate limited state")
    func rateLimited() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess()
        monitor.handleAPIError(.apiError(code: .rateLimited, message: "too many", requestId: "r3"))
        #expect(monitor.health == .rateLimited)
    }

    @Test("Permission denied state")
    func permissionDenied() {
        let monitor = ConnectionHealthMonitor()
        monitor.handleAPIError(.apiError(code: .forbidden, message: "missing scope", requestId: "r4"))
        #expect(monitor.health == .permissionDenied(message: "missing scope"))
    }
}
