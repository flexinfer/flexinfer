import SwiftUI
import LoomCompanionKit

struct ConnectionDiagnosticsView: View {
    @Bindable var connectionVM: ConnectionViewModel
    let healthMonitor: ConnectionHealthMonitor

    private var profile: ConnectionProfile? { TokenStore().loadProfile() }

    private var remediation: ConnectionRemediation {
        ConnectionRemediation.forHealth(healthMonitor.health, mode: profile?.mode)
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                // Profile info
                if let profile {
                    ConnectionProfileView(profile: profile)
                }

                // Health status
                VStack(alignment: .leading, spacing: 12) {
                    Text("Connection Health")
                        .font(.headline)

                    HStack {
                        Image(systemName: healthIcon)
                            .font(.title2)
                            .foregroundStyle(severityColor)
                        VStack(alignment: .leading) {
                            Text(remediation.title)
                                .font(.subheadline)
                                .fontWeight(.medium)
                            Text(remediation.description)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                    }

                    if !remediation.steps.isEmpty {
                        VStack(alignment: .leading, spacing: 6) {
                            Text("Remediation Steps")
                                .font(.caption)
                                .fontWeight(.medium)
                            ForEach(Array(remediation.steps.enumerated()), id: \.offset) { _, step in
                                HStack(alignment: .top, spacing: 6) {
                                    Text("\u{2022}")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                    Text(step)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
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

                // LAN permission guidance when unreachable on local network
                if case .unreachable = healthMonitor.health, profile?.mode == .lan {
                    LANPermissionView()
                }

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

    private var severityColor: Color {
        switch remediation.severity {
        case .ok: return .green
        case .warning: return .orange
        case .error: return .red
        }
    }
}

private struct PingCheck: Decodable {
    let pong: Bool
}
