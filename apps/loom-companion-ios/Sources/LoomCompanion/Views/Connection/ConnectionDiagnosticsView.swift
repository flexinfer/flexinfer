import SwiftUI
import LoomCompanionKit

struct ConnectionDiagnosticsView: View {
    @Bindable var connectionVM: ConnectionViewModel
    let healthMonitor: ConnectionHealthMonitor
    @State private var pushViewModel: PushNotificationsViewModel
    @State private var showAdvanced = false

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
            VStack(spacing: LoomSpacing.lg) {
                primarySection
                advancedSection
            }
            .padding()
        }
        .navigationTitle("Connection")
        .task {
            await pushViewModel.loadPolicy()
        }
    }

    // MARK: - Primary Section

    private var primarySection: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.lg) {
                healthHeader
                if let profile { ConnectionProfileView(profile: profile) }
                remediationBlock
                statusDetails
                actionButtons
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var healthHeader: some View {
        HStack(spacing: LoomSpacing.md) {
            Image(systemName: healthIcon)
                .font(.title)
                .foregroundStyle(severityColor)
                .symbolEffect(.pulse, isActive: healthMonitor.health != .healthy)

            VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                Text(remediation.title)
                    .font(LoomTypography.headlineMedium)
                Text(remediation.description)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.fgSecondary)
            }

            Spacer()
        }
    }

    @ViewBuilder
    private var remediationBlock: some View {
        if !remediation.steps.isEmpty {
            VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                Label("Remediation Steps", systemImage: "wrench.and.screwdriver")
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(LoomColors.fgSecondary)

                ForEach(Array(remediation.steps.enumerated()), id: \.offset) { index, step in
                    HStack(alignment: .top, spacing: LoomSpacing.sm) {
                        Text("\(index + 1).")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.fgMuted)
                            .frame(width: 18, alignment: .trailing)
                        Text(step)
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.fgSecondary)
                    }
                }
            }
            .padding(LoomSpacing.md)
            .background(LoomColors.warningDim)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
    }

    @ViewBuilder
    private var statusDetails: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.sm) {
            if healthMonitor.isPollingFallback {
                Label("Polling fallback active (30s interval)", systemImage: "arrow.triangle.2.circlepath")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.statusDegraded)
            }

            if let lastPing = healthMonitor.lastPingTime {
                Label(
                    "Last ping: \(lastPing.formatted(.relative(presentation: .named)))",
                    systemImage: "clock"
                )
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.fgSecondary)
            }
        }
    }

    private var actionButtons: some View {
        VStack(spacing: LoomSpacing.sm) {
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
            .buttonStyle(.borderedProminent)
            .tint(LoomColors.info)

            Button(role: .destructive) {
                connectionVM.logout()
            } label: {
                Label("Disconnect", systemImage: "arrow.right.square")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.bordered)
        }
    }

    // MARK: - Advanced Section

    private var advancedSection: some View {
        LoomCard {
            DisclosureGroup(isExpanded: $showAdvanced) {
                VStack(alignment: .leading, spacing: LoomSpacing.lg) {
                    lanPermissionBlock
                    sseStateBlock
                    Divider().foregroundStyle(LoomColors.border)
                    pushRegistrationBlock
                    alertPolicyBlock
                }
                .padding(.top, LoomSpacing.md)
            } label: {
                Label {
                    VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                        Text("Advanced")
                            .font(LoomTypography.headlineMedium)
                        Text("Push, alerts, LAN, and SSE diagnostics")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.fgSecondary)
                    }
                } icon: {
                    Image(systemName: "gearshape.2")
                        .foregroundStyle(LoomColors.fgMuted)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    @ViewBuilder
    private var lanPermissionBlock: some View {
        if case .unreachable = healthMonitor.health, profile?.mode == .lan {
            LANPermissionView()
        }
    }

    @ViewBuilder
    private var sseStateBlock: some View {
        if healthMonitor.isPollingFallback {
            VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                Label("SSE Stream", systemImage: "waveform.path")
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(LoomColors.fgSecondary)

                Text("Real-time stream is degraded or disconnected. The app falls back to polling the server every 30 seconds.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.fgSecondary)
            }
        }
    }

    private var pushRegistrationBlock: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.md) {
            Label("Push Notifications", systemImage: "bell.badge")
                .font(LoomTypography.headlineMedium)

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

            HStack(spacing: LoomSpacing.sm) {
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
                .disabled(
                    pushViewModel.pushToken
                        .trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        || pushViewModel.isRegistering
                )

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
                .disabled(
                    pushViewModel.pushToken
                        .trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        || pushViewModel.isUnregistering
                )
            }

            pushStatusMessages
        }
    }

    @ViewBuilder
    private var pushStatusMessages: some View {
        if let statusMessage = pushViewModel.statusMessage, !statusMessage.isEmpty {
            Label(statusMessage, systemImage: "info.circle")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.fgSecondary)
        }

        if let errorMessage = pushViewModel.errorMessage, !errorMessage.isEmpty {
            Label(errorMessage, systemImage: "exclamationmark.triangle")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.statusCritical)
        }
    }

    private var alertPolicyBlock: some View {
        VStack(alignment: .leading, spacing: LoomSpacing.md) {
            HStack {
                Label("Alert Policy", systemImage: "bell.and.waves.left.and.right")
                    .font(LoomTypography.headlineMedium)

                Spacer()

                Button {
                    Task { await pushViewModel.loadPolicy() }
                } label: {
                    if pushViewModel.isLoadingPolicy {
                        ProgressView()
                    } else {
                        Image(systemName: "arrow.clockwise")
                    }
                }
                .buttonStyle(.borderless)
                .disabled(pushViewModel.isLoadingPolicy)
            }

            if !pushViewModel.policyVersion.isEmpty {
                Text("Version: \(pushViewModel.policyVersion)")
                    .font(LoomTypography.monoSmall)
                    .foregroundStyle(LoomColors.fgMuted)
            }

            if !pushViewModel.policyEntries.isEmpty {
                VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                    ForEach(Array(pushViewModel.policyEntries.prefix(6))) { entry in
                        HStack(spacing: LoomSpacing.sm) {
                            Circle()
                                .fill(LoomColors.info)
                                .frame(width: 6, height: 6)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(entry.title)
                                    .font(LoomTypography.labelSmall)
                                Text("\(entry.eventType) \u{2022} \(entry.severity) \u{2022} \(entry.interruptionLevel)")
                                    .font(LoomTypography.monoCaption)
                                    .foregroundStyle(LoomColors.fgMuted)
                            }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Health Mapping

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
