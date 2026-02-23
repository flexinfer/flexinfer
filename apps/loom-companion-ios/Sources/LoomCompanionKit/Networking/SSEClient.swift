import Foundation

/// SSE connection state.
public enum SSEConnectionState: Sendable, Equatable {
    case disconnected
    case connecting
    case connected
    case reconnecting(delay: TimeInterval)
}

/// Parsed SSE event from the stream.
public struct SSEEvent: Sendable {
    public let id: String?
    public let type: String
    public let data: String

    public init(id: String? = nil, type: String, data: String) {
        self.id = id
        self.type = type
        self.data = data
    }
}

/// AsyncStream-based SSE client with exponential backoff reconnection.
/// Matches reconnect constants from bridge/events.go (1s base, 30s max, 2x).
public final class SSEClient: Sendable {
    private let request: URLRequest
    private let session: URLSession

    public let events: AsyncStream<SSEEvent>
    private let continuation: AsyncStream<SSEEvent>.Continuation

    nonisolated(unsafe) private var task: Task<Void, Never>?
    nonisolated(unsafe) private var _connectionState: SSEConnectionState = .disconnected

    public var connectionState: SSEConnectionState { _connectionState }

    /// Callback invoked on connection state changes. Set before calling `connect()`.
    nonisolated(unsafe) public var onStateChange: (@Sendable (SSEConnectionState) -> Void)?

    // Reconnect constants (match bridge/events.go:100-103)
    private static let baseDelay: TimeInterval = 1.0
    private static let maxDelay: TimeInterval = 30.0

    public init(request: URLRequest, session: URLSession = .shared) {
        self.request = request
        self.session = session

        var cont: AsyncStream<SSEEvent>.Continuation!
        self.events = AsyncStream { cont = $0 }
        self.continuation = cont
    }

    public func connect() {
        task = Task { [weak self] in
            await self?.connectLoop()
        }
    }

    public func disconnect() {
        task?.cancel()
        task = nil
        updateState(.disconnected)
    }

    private func connectLoop() async {
        var delay = Self.baseDelay

        while !Task.isCancelled {
            updateState(.connecting)
            do {
                try await stream()
                delay = Self.baseDelay // Reset on clean disconnect
            } catch {
                if Task.isCancelled { return }
                updateState(.reconnecting(delay: delay))
            }
            do {
                try await Task.sleep(for: .seconds(delay))
            } catch {
                return // Cancelled
            }
            delay = min(delay * 2, Self.maxDelay)
        }
    }

    private func stream() async throws {
        let (bytes, response) = try await session.bytes(for: request)

        guard let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 else {
            throw SSEError.badStatus
        }

        updateState(.connected)

        var eventId: String?
        var eventType: String = "message"
        var dataLines: [String] = []

        for try await line in bytes.lines {
            if Task.isCancelled { return }

            if line.isEmpty {
                // Empty line = event boundary
                if !dataLines.isEmpty {
                    let event = SSEEvent(
                        id: eventId,
                        type: eventType,
                        data: dataLines.joined(separator: "\n")
                    )
                    continuation.yield(event)
                }
                eventId = nil
                eventType = "message"
                dataLines = []
                continue
            }

            if line.hasPrefix("data:") {
                let value = line.dropFirst(5)
                dataLines.append(value.hasPrefix(" ") ? String(value.dropFirst()) : String(value))
            } else if line.hasPrefix("event:") {
                let value = line.dropFirst(6)
                eventType = (value.hasPrefix(" ") ? String(value.dropFirst()) : String(value))
            } else if line.hasPrefix("id:") {
                let value = line.dropFirst(3)
                eventId = (value.hasPrefix(" ") ? String(value.dropFirst()) : String(value))
            }
            // Lines starting with ':' are comments, ignore them
        }
    }

    private func updateState(_ state: SSEConnectionState) {
        _connectionState = state
        onStateChange?(state)
    }
}

enum SSEError: Error {
    case badStatus
}

// MARK: - SSE Line Parser (standalone, for testing)

/// Parse a raw SSE text block into events. Used for testing.
public func parseSSEBlock(_ text: String) -> [SSEEvent] {
    var events: [SSEEvent] = []
    var eventId: String?
    var eventType = "message"
    var dataLines: [String] = []

    for line in text.components(separatedBy: "\n") {
        if line.isEmpty {
            if !dataLines.isEmpty {
                events.append(SSEEvent(id: eventId, type: eventType, data: dataLines.joined(separator: "\n")))
            }
            eventId = nil
            eventType = "message"
            dataLines = []
            continue
        }

        if line.hasPrefix("data: ") {
            dataLines.append(String(line.dropFirst(6)))
        } else if line.hasPrefix("data:") {
            dataLines.append(String(line.dropFirst(5)))
        } else if line.hasPrefix("event: ") {
            eventType = String(line.dropFirst(7))
        } else if line.hasPrefix("event:") {
            eventType = String(line.dropFirst(6))
        } else if line.hasPrefix("id: ") {
            eventId = String(line.dropFirst(4))
        } else if line.hasPrefix("id:") {
            eventId = String(line.dropFirst(3))
        }
    }

    return events
}
