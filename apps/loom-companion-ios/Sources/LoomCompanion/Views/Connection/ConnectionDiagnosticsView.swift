import SwiftUI
import LoomCompanionKit

struct ConnectionDiagnosticsView: View {
    @Bindable var connectionVM: ConnectionViewModel
    let healthMonitor: ConnectionHealthMonitor

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                // Profile info
                if let profile = TokenStore().loadProfile() {
                    ConnectionProfileView(profile: profile)
                }

                // Health status
                VStack(alignment: .leading, spacing: 12) {
                    Text("Connection Health")
                        .font(.headline)

                    HStack {
                        Image(systemName: healthIcon)
                            .font(.title2)
                            .foregroundStyle(healthColor)
                        VStack(alignment: .leading) {
                            Text(healthLabel)
                                .font(.subheadline)
                                .fontWeight(.medium)
                            Text(healthDescription)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                    }

                    if healthMonitor.isPollingFallback {
                        HStack(spacing: 4) {
                            Image(systemName: "arrow.triangle.2.circlepath")
                                .font(.caption)
                            Text("Polling fallback active (30s interval)")
                                .font(.caption)
                        }
                        .foregroundStyle(.orange)
                    }

                    if let lastPing = healthMonitor.lastPingTime {
                        HStack(spacing: 4) {
                            Image(systemName: "clock")
                                .font(.caption)
                            Text("Last ping: \(lastPing.formatted(.relative(presentation: .named)))")
                                .font(.caption)
                        }
                        .foregroundStyle(.secondary)
                    }
                }
                .padding()
                .background(.regularMaterial)
                .clipShape(RoundedRectangle(cornerRadius: 12))

                // Actions
                VStack(spacing: 12) {
                    Button {
                        Task {
                            if let client = connectionVM.buildAPIClient() {
                                do {
                                    let _: PingCheck = try await client.request(.ping)
                                    healthMonitor.recordPing()
                                } catch let error as LoomAPIError {
                                    healthMonitor.handleAPIError(error)
                                } catch {}
                            }
                        }
                    } label: {
                        Label("Test Connection", systemImage: "antenna.radiowaves.left.and.right")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)

                    Button(role: .destructive) {
                        connectionVM.logout()
                    } label: {
                        Label("Disconnect", systemImage: "arrow.right.square")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                }
            }
            .padding()
        }
        .navigationTitle("Connection")
    }

    private var healthIcon: String {
        switch healthMonitor.health {
        case .healthy: return "checkmark.circle.fill"
        case .degradedStream: return "wifi.exclamationmark"
        case .authFailure: return "lock.shield"
        case .permissionDenied: return "hand.raised.fill"
        case .unreachable: return "wifi.slash"
        case .rateLimited: return "gauge.with.dots.needle.67percent"
        case .unknown: return "questionmark.circle"
        }
    }

    private var healthColor: Color {
        switch healthMonitor.health {
        case .healthy: return .green
        case .degradedStream, .rateLimited: return .orange
        case .authFailure, .permissionDenied, .unreachable: return .red
        case .unknown: return .secondary
        }
    }

    private var healthLabel: String {
        switch healthMonitor.health {
        case .healthy: return "Connected"
        case .degradedStream: return "Degraded"
        case .authFailure: return "Auth Failed"
        case .permissionDenied: return "Permission Denied"
        case .unreachable: return "Unreachable"
        case .rateLimited: return "Rate Limited"
        case .unknown: return "Unknown"
        }
    }

    private var healthDescription: String {
        switch healthMonitor.health {
        case .healthy: return "REST and SSE connections are working normally."
        case .degradedStream: return "REST API works but real-time stream is disconnected."
        case let .authFailure(msg): return msg
        case let .permissionDenied(msg): return msg
        case .unreachable: return "Cannot reach the server. Check network and URL."
        case .rateLimited: return "Too many requests. Will resume automatically."
        case .unknown: return "No connection probe has been completed yet."
        }
    }
}

private struct PingCheck: Decodable {
    let pong: Bool
}
