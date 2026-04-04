import SwiftUI
import LoomCompanionKit

struct ConnectionDiagnosticsView: View {
    @Bindable var connectionVM: ConnectionViewModel
    let healthMonitor: ConnectionHealthMonitor
    @State private var pushViewModel: PushNotificationsViewModel
    @State private var showAdvancedControls = false

    private var profile: ConnectionProfile? { TokenStore().loadProfile() }

    private var remediation: ConnectionRemediation {
        ConnectionRemediation.forHealth(healthMonitor.health, mode: profile?.mode)
    }

    init(connectionVM: ConnectionViewModel, healthMonitor: ConnectionHealthMonitor) {
        self.connectionVM = connectionVM
        self.healthMonitor = healthMonitor
        _pushViewModel = State(initialValue: PushNotificationsViewModel(apiClient: connectionVM.buildAPIClient()))
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                LoomCard {
                    VStack(alignment: .leading, spacing: 12) {
                        Text("Connection Status")
                            .font(.headline)

                        if let profile {
                            ConnectionProfileView(profile: profile)
                        }

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
                                    .foregroundStyle(LoomColors.fgSecondary)
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
                                            .foregroundStyle(LoomColors.fgSecondary)
                                        Text(step)
                                            .font(.caption)
                                            .foregroundStyle(LoomColors.fgSecondary)
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
                            .foregroundStyle(LoomColors.statusDegraded)
                        }

                        if let lastPing = healthMonitor.lastPingTime {
                            HStack(spacing: 4) {
                                Image(systemName: "clock")
                                    .font(.caption)
                                Text("Last ping: \(lastPing.formatted(.relative(presentation: .named)))")
                                    .font(.caption)
                            }
                            .foregroundStyle(LoomColors.fgSecondary)
                        }

                        if case .unreachable = healthMonitor.health, profile?.mode == .lan {
                            LANPermissionView()
                        }

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
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }

                LoomCard {
                    VStack(alignment: .leading, spacing: 12) {
                        DisclosureGroup(isExpanded: $showAdvancedControls) {
                            VStack(alignment: .leading, spacing: 12) {
                                VStack(alignment: .leading, spacing: 12) {
                                    Text("Push Notifications")
                                        .font(.headline)

                                    TextField("Push token", text: $pushViewModel.pushToken)
                                        .textFieldStyle(.roundedBorder)
                                        .autocorrectionDisabled()
                                        #if os(iOS)
                                        .textInputAutocapitalization(.never)
                                        #endif

                                    Picker("Platform", selection: $pushViewModel.platform) {
                                        Text("APNs").tag(PushPlatform.apns)
                                        Text("FCM").tag(PushPlatform.fcm)
                                    }
                                    .pickerStyle(.segmented)

                                    HStack(spacing: 8) {
                                        Button {
                                            Task { await pushViewModel.registerPushToken() }
                                        } label: {
                                            if pushViewModel.isRegistering {
                                                ProgressView()
                                            } else {
                                                Text("Register")
                                            }
                                        }
                                        .buttonStyle(.borderedProminent)
                                        .disabled(pushViewModel.pushToken.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || pushViewModel.isRegistering)

                                        Button {
                                            Task { await pushViewModel.unregisterPushToken() }
                                        } label: {
                                            if pushViewModel.isUnregistering {
                                                ProgressView()
                                            } else {
                                                Text("Unregister")
                                            }
                                        }
                                        .buttonStyle(.bordered)
                                        .disabled(pushViewModel.pushToken.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || pushViewModel.isUnregistering)
                                    }

                                    HStack(spacing: 8) {
                                        Button {
                                            Task { await pushViewModel.loadPolicy() }
                                        } label: {
                                            if pushViewModel.isLoadingPolicy {
                                                ProgressView()
                                            } else {
                                                Label("Refresh Policy", systemImage: "arrow.clockwise")
                                            }
                                        }
                                        .buttonStyle(.bordered)
                                        .disabled(pushViewModel.isLoadingPolicy)

                                        if !pushViewModel.policyVersion.isEmpty {
                                            Text("Policy \(pushViewModel.policyVersion)")
                                                .font(.caption)
                                                .foregroundStyle(LoomColors.fgSecondary)
                                        }
                                    }

                                    if let statusMessage = pushViewModel.statusMessage, !statusMessage.isEmpty {
                                        Text(statusMessage)
                                            .font(.caption)
                                            .foregroundStyle(LoomColors.fgSecondary)
                                    }

                                    if let errorMessage = pushViewModel.errorMessage, !errorMessage.isEmpty {
                                        Text(errorMessage)
                                            .font(.caption)
                                            .foregroundStyle(LoomColors.statusCritical)
                                    }

                                    if !pushViewModel.policyEntries.isEmpty {
                                        VStack(alignment: .leading, spacing: 6) {
                                            Text("Alert Policy Snapshot")
                                                .font(.caption)
                                                .fontWeight(.medium)
                                            ForEach(Array(pushViewModel.policyEntries.prefix(6))) { entry in
                                                VStack(alignment: .leading, spacing: 2) {
                                                    Text(entry.title)
                                                        .font(.caption)
                                                        .fontWeight(.semibold)
                                                    Text("\(entry.eventType) • \(entry.severity) • \(entry.interruptionLevel)")
                                                        .font(.caption2)
                                                        .foregroundStyle(LoomColors.fgSecondary)
                                                }
                                            }
                                        }
                                    }
                                }

                                Divider()

                                Button(role: .destructive) {
                                    connectionVM.logout()
                                } label: {
                                    Label("Disconnect", systemImage: "arrow.right.square")
                                        .frame(maxWidth: .infinity)
                                }
                                .buttonStyle(.bordered)
                            }
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("Advanced connection controls")
                                        .font(.headline)
                                    Text("Push diagnostics, policy refresh, and disconnect live here.")
                                        .font(.caption)
                                        .foregroundStyle(LoomColors.fgSecondary)
                                }
                                Spacer()
                                Image(systemName: showAdvancedControls ? "chevron.up" : "chevron.down")
                                    .font(.caption2)
                            }
                            .contentShape(Rectangle())
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding()
        }
        .navigationTitle("Connection")
        .task {
            await pushViewModel.loadPolicy()
        }
    }

    private var healthIcon: String {
        switch healthMonitor.health {
        case .healthy: return "checkmark.circle.fill"
        case .degradedStream: return "wifi.exclamationmark"
        case .authFailure: return "lock.shield"
        case .permissionDenied: return "hand.raised.fill"
        case .gatewayRouteMissing: return "arrow.triangle.branch"
        case .unreachable: return "wifi.slash"
        case .rateLimited: return "gauge.with.dots.needle.67percent"
        case .unknown: return "questionmark.circle"
        }
    }

    private var severityColor: Color {
        switch remediation.severity {
        case .ok: return LoomColors.statusHealthy
        case .warning: return LoomColors.statusDegraded
        case .error: return LoomColors.statusCritical
        }
    }
}

private struct PingCheck: Decodable {
    let pong: Bool
}
