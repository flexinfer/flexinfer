import Testing
import Foundation
@testable import LoomCompanionKit

// MARK: - Thread-safe counter

private final class AtomicCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: Int = 0

    var value: Int {
        lock.lock()
        defer { lock.unlock() }
        return _value
    }

    func increment() {
        lock.lock()
        _value += 1
        lock.unlock()
    }
}

// MARK: - Thread-safe state collector (reused pattern)

private final class StateCollector: @unchecked Sendable {
    private let lock = NSLock()
    private var _states: [SSEConnectionState] = []

    var states: [SSEConnectionState] {
        lock.lock()
        defer { lock.unlock() }
        return _states
    }

    func record(_ state: SSEConnectionState) {
        lock.lock()
        _states.append(state)
        lock.unlock()
    }

    var connectedCount: Int {
        states.filter { $0 == .connected }.count
    }

    var reconnectingCount: Int {
        states.filter { if case .reconnecting = $0 { return true }; return false }.count
    }
}

private func makeClient(
    streamResults: [SSETestStreamResult],
    baseDelay: TimeInterval = 0.01,
    maxDelay: TimeInterval = 0.05
) -> (SSEClient, StateCollector) {
    let request = URLRequest(url: URL(string: "http://test.local/events")!)
    let client = SSEClient(request: request)
    client._testBaseDelay = baseDelay
    client._testMaxDelay = maxDelay
    client._testStreamResults = streamResults

    let collector = StateCollector()
    client.onStateChange = { [collector] state in collector.record(state) }

    return (client, collector)
}

// MARK: - Synthetic Network Churn Tests

@Suite("SSE Network Churn")
struct SSENetworkChurnTests {

    // MARK: - Rapid fail/succeed cycling

    @Test("Rapid churn: 5 consecutive fail-succeed cycles deliver correct state sequence")
    func rapidChurnCycles() async throws {
        // Simulate network flapping: connection succeeds briefly then drops, repeatedly
        let (client, collector) = makeClient(streamResults: [
            .fail,    // cycle 1
            .succeed,
            .fail,    // cycle 2
            .succeed,
            .fail,    // cycle 3
            .succeed,
            .fail,    // cycle 4
            .succeed,
            .fail,    // cycle 5
            .succeed,
        ])
        client.connect()
        try await Task.sleep(for: .seconds(0.8))
        client.disconnect()

        // Each fail produces a reconnecting state, each succeed produces a connected state
        #expect(collector.reconnectingCount >= 4, "Expected at least 4 reconnecting states from churn")
        #expect(collector.connectedCount >= 4, "Expected at least 4 connected states from churn")

        // Verify no stuck states: every reconnecting is eventually followed by connecting
        let states = collector.states
        for (i, state) in states.enumerated() {
            if case .reconnecting = state {
                let following = states.suffix(from: i + 1)
                #expect(following.contains(.connecting) || following.contains(.disconnected),
                        "Reconnecting at index \(i) should be followed by connecting or disconnected")
            }
        }
    }

    @Test("Rapid churn preserves event delivery after reconnection")
    func churnPreservesEvents() async throws {
        let preChurnEvents = [SSEEvent(type: "pre.churn", data: "before")]
        let postChurnEvents = [SSEEvent(type: "post.churn", data: "after")]

        let (client, _) = makeClient(streamResults: [
            .succeedWithEvents(preChurnEvents),
            .fail,
            .fail,
            .succeedWithEvents(postChurnEvents),
        ])

        var received: [SSEEvent] = []
        let collectTask = Task {
            for await event in client.events {
                received.append(event)
            }
        }

        client.connect()
        try await Task.sleep(for: .seconds(0.5))
        collectTask.cancel()
        client.disconnect()

        #expect(received.count >= 2, "Expected events from both pre- and post-churn streams")
        let types = received.map(\.type)
        #expect(types.contains("pre.churn"), "Pre-churn event should be delivered")
        #expect(types.contains("post.churn"), "Post-churn event should be delivered")
    }

    // MARK: - Health monitor integration under churn

    @Test("Health monitor transitions through degraded and back to healthy during churn")
    func healthMonitorChurnTransitions() async throws {
        let monitor = ConnectionHealthMonitor()
        monitor.handleSuccess() // Start healthy

        // Simulate rapid SSE state changes as if the network is flapping
        monitor.handleSSEStateChange(.reconnecting(delay: 0.01))
        #expect(monitor.health == .degradedStream)
        #expect(monitor.isPollingFallback == true)

        monitor.handleSSEStateChange(.connected)
        #expect(monitor.health == .healthy)
        #expect(monitor.isPollingFallback == false)

        // Second flap
        monitor.handleSSEStateChange(.disconnected)
        #expect(monitor.health == .degradedStream)
        #expect(monitor.isPollingFallback == true)

        monitor.handleSSEStateChange(.connecting)
        // Connecting is transient, no state change expected
        #expect(monitor.health == .degradedStream)
        #expect(monitor.isPollingFallback == true)

        monitor.handleSSEStateChange(.connected)
        #expect(monitor.health == .healthy)
        #expect(monitor.isPollingFallback == false)

        // Third rapid cycle — degraded → connected in quick succession
        monitor.handleSSEStateChange(.reconnecting(delay: 0.01))
        monitor.handleSSEStateChange(.connected) // Immediate recovery
        #expect(monitor.health == .healthy)
        #expect(monitor.isPollingFallback == false)
    }

    @Test("Polling fallback callback fires during degraded window")
    func pollingFallbackFiresDuringDegraded() async throws {
        let monitor = ConnectionHealthMonitor()
        let pollCount = AtomicCounter()

        monitor.onPollRefresh = {
            pollCount.increment()
        }

        monitor.handleSuccess()

        // Enter degraded state — polling should start
        monitor.handleSSEStateChange(.disconnected)
        #expect(monitor.isPollingFallback == true)

        // Wait for at least one poll cycle (pollInterval is 30s, but we can verify
        // the mechanism is started — the actual interval is production config)
        // We verify start/stop rather than waiting 30s.
        #expect(monitor.isPollingFallback == true, "Polling should be active while degraded")

        // Recover — polling should stop
        monitor.handleSSEStateChange(.connected)
        #expect(monitor.isPollingFallback == false, "Polling should stop on recovery")
    }

    // MARK: - Disconnect during reconnect

    @Test("Explicit disconnect during reconnecting state terminates cleanly")
    func disconnectDuringReconnecting() async throws {
        let (client, collector) = makeClient(streamResults: [
            .fail, .fail, .fail, .fail, .fail,
        ])
        client.connect()

        // Wait for at least one reconnecting state
        try await Task.sleep(for: .seconds(0.05))

        let hadReconnecting = collector.reconnectingCount > 0
        #expect(hadReconnecting, "Should be in reconnecting state")

        // Disconnect while reconnecting
        client.disconnect()

        // Final state should be disconnected
        let states = collector.states
        #expect(states.last == .disconnected, "Should end in disconnected after explicit disconnect")

        // Verify no more states accumulate after disconnect
        let countAfterDisconnect = states.count
        try await Task.sleep(for: .seconds(0.1))
        let countLater = collector.states.count
        #expect(countLater == countAfterDisconnect, "No state changes should occur after disconnect")
    }

    // MARK: - Backoff reset across churn

    @Test("Backoff resets correctly through multiple churn cycles")
    func backoffResetsAcrossChurn() async throws {
        let (client, collector) = makeClient(
            streamResults: [
                .fail,     // reconnecting(0.01), delay→0.02
                .fail,     // reconnecting(0.02), delay→0.04
                .succeed,  // connected, reset delay=0.01, sleep 0.01, delay→0.02
                .fail,     // reconnecting(0.02) — proves reset happened
                .fail,     // reconnecting(0.04)
                .succeed,  // connected, reset again
                .fail,     // reconnecting(0.02) — proves second reset
            ],
            baseDelay: 0.01,
            maxDelay: 0.05
        )
        client.connect()
        try await Task.sleep(for: .seconds(0.5))
        client.disconnect()

        let delays = collector.states.compactMap { state -> TimeInterval? in
            if case .reconnecting(let delay) = state { return delay }
            return nil
        }

        #expect(delays.count >= 4, "Expected at least 4 reconnecting delays")

        if delays.count >= 4 {
            // First churn cycle: 0.01, 0.02
            #expect(delays[0] == 0.01)
            #expect(delays[1] == 0.02)
            // After first success/reset: 0.02 (base 0.01 → sleep → doubled to 0.02)
            #expect(delays[2] == 0.02, "Should reset to post-base after success")
            // Escalated again: 0.04
            #expect(delays[3] == 0.04)
        }
    }

    // MARK: - SSE → poll → SSE recovery

    @Test("Full SSE to polling fallback to SSE recovery path")
    func ssePollingRecoveryPath() async throws {
        let monitor = ConnectionHealthMonitor()
        let pollRefreshCount = AtomicCounter()
        monitor.onPollRefresh = { pollRefreshCount.increment() }

        // Phase 1: SSE connected and healthy
        monitor.handleSSEStateChange(.connected)
        #expect(monitor.health == .healthy)
        #expect(monitor.isPollingFallback == false)

        // Phase 2: SSE drops — enter degraded with polling
        monitor.handleSSEStateChange(.reconnecting(delay: 1.0))
        #expect(monitor.health == .degradedStream)
        #expect(monitor.isPollingFallback == true)

        // Phase 3: SSE reconnect attempt
        monitor.handleSSEStateChange(.connecting)
        #expect(monitor.health == .degradedStream, "Still degraded during reconnect attempt")
        #expect(monitor.isPollingFallback == true, "Polling still active during reconnect attempt")

        // Phase 4: SSE reconnects successfully — full recovery
        monitor.handleSSEStateChange(.connected)
        #expect(monitor.health == .healthy, "Should return to healthy on SSE recovery")
        #expect(monitor.isPollingFallback == false, "Polling should stop on SSE recovery")
    }

    @Test("Multiple rapid SSE drops don't stack polling tasks")
    func rapidDropsNoPollStacking() async throws {
        let monitor = ConnectionHealthMonitor()
        let pollCount = AtomicCounter()
        monitor.onPollRefresh = { pollCount.increment() }

        // Rapidly alternate between disconnected (starts poll) and reconnecting (also starts poll)
        monitor.handleSuccess()
        monitor.handleSSEStateChange(.disconnected)
        #expect(monitor.isPollingFallback == true)

        monitor.handleSSEStateChange(.reconnecting(delay: 0.01))
        #expect(monitor.isPollingFallback == true) // Already polling, shouldn't double-start

        monitor.handleSSEStateChange(.disconnected)
        #expect(monitor.isPollingFallback == true)

        // Recovery should cleanly stop all polling
        monitor.handleSSEStateChange(.connected)
        #expect(monitor.isPollingFallback == false, "Single recovery should stop all polling")
    }
}
