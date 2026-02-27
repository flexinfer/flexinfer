import Foundation

/// Connection health states.
public enum ConnectionHealth: Sendable, Equatable {
    case unknown
    case healthy
    case degradedStream
    case authFailure(message: String)
    case permissionDenied(message: String)
    case gatewayRouteMissing(message: String)
    case unreachable
    case rateLimited
}

/// Monitors connection health by observing API responses and SSE state.
/// Manages polling fallback when SSE is degraded.
@Observable
public final class ConnectionHealthMonitor {
    public var health: ConnectionHealth = .unknown
    public var isPollingFallback: Bool = false
    public var lastPingTime: Date?

    private var pollTask: Task<Void, Never>?

    /// Polling interval matches HUD web frontend (30s).
    public static let pollInterval: TimeInterval = 30.0

    /// Callback for polling fallback refresh. Set by the ViewModel layer.
    @ObservationIgnored
    public var onPollRefresh: (() async -> Void)?

    public init() {}

    /// Handle a successful API response.
    public func handleSuccess() {
        switch health {
        case .unknown, .unreachable, .rateLimited, .gatewayRouteMissing:
            health = .healthy
        case .authFailure, .permissionDenied:
            health = .healthy
        case .healthy, .degradedStream:
            break
        }
    }

    /// Handle an API error.
    public func handleAPIError(_ error: LoomAPIError) {
        switch error {
        case let .apiError(code, message, _):
            switch code {
            case .unauthorized, .tokenRevoked:
                health = .authFailure(message: message)
                stopPolling()
            case .forbidden:
                health = .permissionDenied(message: message)
                stopPolling()
            case .notFound:
                let detail = message.isEmpty || message == "Not found"
                    ? "The gateway did not route /api/mobile/v1 to the mobile API backend."
                    : message
                health = .gatewayRouteMissing(message: detail)
                stopPolling()
            case .rateLimited:
                health = .rateLimited
            default:
                break
            }
        case .networkError:
            health = .unreachable
            stopPolling()
        case .noToken:
            health = .authFailure(message: "No token configured")
            stopPolling()
        default:
            break
        }
    }

    /// Handle SSE connection state changes.
    public func handleSSEStateChange(_ state: SSEConnectionState) {
        switch state {
        case .connected:
            stopPolling()
            if health == .degradedStream || health == .unknown {
                health = .healthy
            }
        case .reconnecting:
            if health == .healthy {
                health = .degradedStream
            }
            startPolling()
        case .disconnected:
            if health == .healthy {
                health = .degradedStream
            }
            startPolling()
        case .connecting:
            break
        }
    }

    /// Record a successful ping.
    public func recordPing() {
        lastPingTime = Date()
        handleSuccess()
    }

    // MARK: - Polling Fallback

    private func startPolling() {
        guard !isPollingFallback else { return }
        isPollingFallback = true
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: .seconds(Self.pollInterval))
                } catch {
                    return
                }
                await self?.onPollRefresh?()
            }
        }
    }

    private func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
        isPollingFallback = false
    }
}
