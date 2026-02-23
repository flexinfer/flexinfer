import Testing
import Foundation
@testable import LoomCompanionKit

// MARK: - Thread-safe state collector

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

    var reconnectingDelays: [TimeInterval] {
        states.compactMap { state in
            if case .reconnecting(let delay) = state { return delay }
            return nil
        }
    }
}

// MARK: - Test helpers

/// Create an SSEClient with test stream results and fast delays.
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

// MARK: - Tests

@Suite("SSE Client Reconnect")
struct SSEClientReconnectTests {

    @Test("Initial state is disconnected")
    func initialState() {
        let request = URLRequest(url: URL(string: "http://test.local/events")!)
        let client = SSEClient(request: request)
        #expect(client.connectionState == .disconnected)
    }

    @Test("Successful connection transitions to connected")
    func connectSuccess() async throws {
        let (client, collector) = makeClient(streamResults: [.succeed])
        client.connect()
        try await Task.sleep(for: .seconds(0.1))
        client.disconnect()

        let states = collector.states
        #expect(states.contains(.connecting))
        #expect(states.contains(.connected))
    }

    @Test("Connection failure triggers reconnecting state")
    func connectionFailureReconnects() async throws {
        let (client, collector) = makeClient(streamResults: [.fail, .fail])
        client.connect()
        try await Task.sleep(for: .seconds(0.15))
        client.disconnect()

        let delays = collector.reconnectingDelays
        #expect(delays.count >= 1, "Expected at least one reconnecting state")
        #expect(delays[0] == 0.01, "First reconnect delay should be baseDelay")
    }

    @Test("Exponential backoff doubles delay each failure")
    func exponentialBackoff() async throws {
        let (client, collector) = makeClient(streamResults: [
            .fail, .fail, .fail, .fail, .fail,
        ])
        client.connect()
        // Wait for several retry cycles: 10+20+40+50+50 = 170ms + overhead
        try await Task.sleep(for: .seconds(0.5))
        client.disconnect()

        let delays = collector.reconnectingDelays
        #expect(delays.count >= 3, "Expected at least 3 reconnecting states")

        if delays.count >= 1 { #expect(delays[0] == 0.01, "First delay = baseDelay") }
        if delays.count >= 2 { #expect(delays[1] == 0.02, "Second delay = 2x baseDelay") }
        if delays.count >= 3 { #expect(delays[2] == 0.04, "Third delay = 4x baseDelay") }
    }

    @Test("Backoff delay caps at configured max")
    func backoffCap() async throws {
        let (client, collector) = makeClient(
            streamResults: Array(repeating: SSETestStreamResult.fail, count: 10),
            baseDelay: 0.01,
            maxDelay: 0.04
        )
        client.connect()
        try await Task.sleep(for: .seconds(0.5))
        client.disconnect()

        let delays = collector.reconnectingDelays
        #expect(delays.count >= 4, "Expected at least 4 reconnecting states")

        for delay in delays {
            #expect(delay <= 0.04, "Delay \(delay) exceeds configured max 0.04")
        }
        #expect(delays.contains(0.04), "Expected at least one delay at the cap")
    }

    @Test("Delay resets after successful stream")
    func delayResetsOnSuccess() async throws {
        let (client, collector) = makeClient(streamResults: [
            .fail,     // reconnecting(0.01), sleep 0.01, delay→0.02
            .fail,     // reconnecting(0.02), sleep 0.02, delay→0.04
            .succeed,  // connected, delay=0.01 (reset), sleep 0.01, delay→0.02
            .fail,     // reconnecting(0.02) — reset from 0.04 to 0.02
        ])
        client.connect()
        try await Task.sleep(for: .seconds(0.3))
        client.disconnect()

        let delays = collector.reconnectingDelays
        #expect(delays.count >= 3, "Expected 3 reconnecting states")

        if delays.count >= 3 {
            #expect(delays[0] == 0.01, "First failure delay")
            #expect(delays[1] == 0.02, "Second failure delay (escalated)")
            // Without reset, third would be 0.04+. With reset, it's 0.02.
            #expect(delays[2] == 0.02, "Third delay should be reset (0.02 not 0.04+)")
        }
    }

    @Test("Disconnect cancels reconnect loop and sets disconnected")
    func disconnectStopsLoop() async throws {
        let (client, collector) = makeClient(streamResults: [.fail])
        client.connect()
        try await Task.sleep(for: .seconds(0.05))
        client.disconnect()

        let states = collector.states
        #expect(states.last == .disconnected)
    }

    @Test("State transition sequence: connecting → reconnecting → connecting → connected")
    func stateTransitionSequence() async throws {
        let (client, collector) = makeClient(streamResults: [
            .fail,    // connecting → reconnecting
            .succeed, // connecting → connected
        ])
        client.connect()
        try await Task.sleep(for: .seconds(0.15))
        client.disconnect()

        let states = collector.states
        #expect(states.count >= 4, "Expected at least 4 state transitions")

        #expect(states[0] == .connecting)
        if case .reconnecting = states[1] {
            // OK — failure triggers reconnecting
        } else {
            Issue.record("Expected reconnecting at index 1, got \(states[1])")
        }
        #expect(states[2] == .connecting, "Should retry after reconnecting")
        #expect(states[3] == .connected, "Second attempt should succeed")
    }

    @Test("Events are yielded through the async stream")
    func eventsYielded() async throws {
        let testEvents = [
            SSEEvent(type: "agent.session.start", data: "{\"session_id\":\"s1\"}"),
            SSEEvent(type: "hud.health", data: "{\"healthy\":true}"),
        ]
        let (client, _) = makeClient(streamResults: [
            .succeedWithEvents(testEvents),
        ])

        client.connect()

        var received: [SSEEvent] = []
        let collectTask = Task {
            for await event in client.events {
                received.append(event)
                if received.count >= 2 { break }
            }
        }

        try await Task.sleep(for: .seconds(0.2))
        collectTask.cancel()
        client.disconnect()

        #expect(received.count >= 2, "Expected at least 2 events yielded")
        if received.count >= 2 {
            #expect(received[0].type == "agent.session.start")
            #expect(received[0].data == "{\"session_id\":\"s1\"}")
            #expect(received[1].type == "hud.health")
        }
    }

    @Test("Multiple successful streams each reset delay independently")
    func multipleSuccessResets() async throws {
        let (client, collector) = makeClient(streamResults: [
            .fail,     // reconnecting(0.01)
            .succeed,  // connected, reset
            .fail,     // reconnecting(0.02) — reset from escalated
            .succeed,  // connected, reset again
            .fail,     // reconnecting(0.02) — reset again
        ])
        client.connect()
        try await Task.sleep(for: .seconds(0.3))
        client.disconnect()

        let delays = collector.reconnectingDelays
        #expect(delays.count >= 3)

        // Each post-success failure should show the same reset delay (0.02).
        if delays.count >= 3 {
            #expect(delays[1] == delays[2],
                    "Post-success failure delays should be equal (both reset)")
        }
    }
}
