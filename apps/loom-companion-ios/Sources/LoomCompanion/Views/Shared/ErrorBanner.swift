import SwiftUI
import LoomCompanionKit

struct ErrorBanner: View {
    let health: ConnectionHealth
    let onRetry: (() -> Void)?

    init(health: ConnectionHealth, onRetry: (() -> Void)? = nil) {
        self.health = health
        self.onRetry = onRetry
    }

    var body: some View {
        if let message = bannerMessage {
            HStack {
                Image(systemName: bannerIcon)
                    .foregroundStyle(bannerColor)
                Text(message)
                    .font(.caption)
                Spacer()
                if let onRetry {
                    Button("Retry", action: onRetry)
                        .font(.caption)
                        .buttonStyle(.bordered)
                }
            }
            .padding(10)
            .background(bannerColor.opacity(0.1))
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
    }

    private var bannerMessage: String? {
        switch health {
        case .healthy, .unknown:
            return nil
        case .degradedStream:
            return "Real-time updates unavailable. Using polling fallback."
        case let .authFailure(message):
            return "Authentication failed: \(message)"
        case let .permissionDenied(message):
            return "Permission denied: \(message)"
        case let .gatewayRouteMissing(message):
            return "Gateway route missing: \(message)"
        case .unreachable:
            return "Server unreachable. Check your connection."
        case .rateLimited:
            return "Rate limited. Requests will resume shortly."
        }
    }

    private var bannerIcon: String {
        switch health {
        case .degradedStream: return "wifi.exclamationmark"
        case .authFailure: return "lock.shield"
        case .permissionDenied: return "hand.raised"
        case .gatewayRouteMissing: return "arrow.triangle.branch"
        case .unreachable: return "wifi.slash"
        case .rateLimited: return "gauge.with.dots.needle.67percent"
        default: return "info.circle"
        }
    }

    private var bannerColor: Color {
        switch health {
        case .degradedStream, .rateLimited: return .orange
        case .authFailure, .permissionDenied, .gatewayRouteMissing: return .red
        case .unreachable: return .red
        default: return .secondary
        }
    }
}
