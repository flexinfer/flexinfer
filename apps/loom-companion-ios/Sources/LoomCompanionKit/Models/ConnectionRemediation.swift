import Foundation

/// Remediation guidance for a connection health state.
public struct ConnectionRemediation: Sendable, Equatable {
    public let title: String
    public let description: String
    public let steps: [String]
    public let severity: Severity

    public enum Severity: String, Sendable {
        case ok
        case warning
        case error
    }

    public init(title: String, description: String, steps: [String], severity: Severity) {
        self.title = title
        self.description = description
        self.steps = steps
        self.severity = severity
    }

    /// Map a connection health state to remediation guidance.
    /// The optional `mode` parameter adds LAN-specific or gateway-specific steps.
    public static func forHealth(_ health: ConnectionHealth, mode: ConnectionMode? = nil) -> ConnectionRemediation {
        switch health {
        case .healthy:
            return ConnectionRemediation(
                title: "Connected",
                description: "REST and SSE connections are working normally.",
                steps: [],
                severity: .ok
            )
        case .degradedStream:
            return ConnectionRemediation(
                title: "Stream Degraded",
                description: "REST API responds but the real-time event stream is disconnected. Data will refresh via polling (30s interval).",
                steps: degradedStreamSteps(mode: mode),
                severity: .warning
            )
        case .authFailure(let message):
            return ConnectionRemediation(
                title: "Authentication Failed",
                description: message.isEmpty ? "The server rejected the bearer token." : message,
                steps: authFailureSteps(mode: mode),
                severity: .error
            )
        case .permissionDenied(let message):
            return ConnectionRemediation(
                title: "Permission Denied",
                description: message.isEmpty ? "The token lacks a required scope." : message,
                steps: permissionDeniedSteps(),
                severity: .error
            )
        case .gatewayRouteMissing(let message):
            return ConnectionRemediation(
                title: "Gateway Route Missing",
                description: message.isEmpty ? "The gateway is reachable, but /api/mobile/v1 is not routed to the mobile backend." : message,
                steps: gatewayRouteMissingSteps(),
                severity: .error
            )
        case .unreachable:
            return ConnectionRemediation(
                title: "Server Unreachable",
                description: "Cannot establish a connection to the Loom HUD server.",
                steps: unreachableSteps(mode: mode),
                severity: .error
            )
        case .rateLimited:
            return ConnectionRemediation(
                title: "Rate Limited",
                description: "Too many requests sent. The app will resume automatically after the rate limit window resets.",
                steps: [
                    "Wait for the rate limit window to reset (1 minute).",
                    "Reduce polling frequency if manually refreshing.",
                ],
                severity: .warning
            )
        case .unknown:
            return ConnectionRemediation(
                title: "Not Tested",
                description: "No connection probe has completed yet.",
                steps: [
                    "Tap \"Test Connection\" to check connectivity.",
                ],
                severity: .warning
            )
        }
    }

    // MARK: - Per-state step builders

    private static func degradedStreamSteps(mode: ConnectionMode?) -> [String] {
        var steps = [
            "Check that the Loom HUD server is running.",
            "The SSE stream will reconnect automatically with exponential backoff.",
        ]
        if mode == .lan {
            steps.append("Verify both devices are on the same local network.")
            steps.append("Check that iOS Local Network permission is enabled for Loom Companion.")
        }
        if mode == .gateway {
            steps.append("Check that the gateway proxy is healthy and forwarding traffic.")
        }
        return steps
    }

    private static func authFailureSteps(mode: ConnectionMode?) -> [String] {
        var steps = [
            "Verify the bearer token matches HUD_MOBILE_OPERATOR_TOKEN on the server.",
            "Check if the token has been revoked via the admin endpoint.",
            "Re-pair the connection with a fresh token.",
        ]
        if mode == .gateway {
            steps.append("Ensure the gateway is not stripping or rewriting the Authorization header.")
            steps.append("If Cloudflare Access is enabled, configure CF-Access-Client-Id and CF-Access-Client-Secret in Connection settings.")
        }
        return steps
    }

    private static func permissionDeniedSteps() -> [String] {
        [
            "The token is valid but lacks a required scope for this operation.",
            "Check HUD_MOBILE_OPERATOR_SCOPES on the server configuration.",
            "Required scopes: mobile:read, mobile:session:create, mobile:session:end.",
        ]
    }

    private static func gatewayRouteMissingSteps() -> [String] {
        [
            "The gateway host is up, but mobile API routing is missing.",
            "Route /api/mobile/v1 to the mobile-hud service in ingress.",
            "Re-run mobile gateway preflight and then retry connection.",
        ]
    }

    private static func unreachableSteps(mode: ConnectionMode?) -> [String] {
        if mode == .lan {
            return [
                "Check that both devices are on the same local network.",
                "Verify the server URL and port are correct.",
                "Check iOS Settings > Privacy & Security > Local Network and enable Loom Companion.",
                "Verify the Loom HUD server is running (loom hud --serve).",
                "Check for firewall rules blocking the connection.",
            ]
        }
        if mode == .gateway {
            return [
                "Check your internet connection.",
                "Verify the gateway URL is correct.",
                "Check that the gateway proxy is running and reachable.",
                "Verify TLS certificate validity if using HTTPS.",
            ]
        }
        return [
            "Check your network connection.",
            "Verify the server URL is correct.",
            "Ensure the Loom HUD server is running.",
        ]
    }
}
