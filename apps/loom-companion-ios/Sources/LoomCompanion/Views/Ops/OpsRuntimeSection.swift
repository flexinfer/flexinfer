import SwiftUI
import LoomCompanionKit

/// Runtime section: sandbox start/stop, spawn agent, presence agents, claims/worktrees, gateway/daemon.
struct OpsRuntimeSection: View {
    @Bindable var viewModel: OpsViewModel
    var broadcaster: SSEEventBroadcaster?

    @State private var agentDisplayLimit = 8
    @State private var startSandboxProject = ""
    @State private var startSandboxAgentID = ""
    @State private var showSandboxStartConfirmation = false

    var body: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            spawnLauncherCard
                .cardAppear(index: 0)

            presenceCard
                .cardAppear(index: 1)

            claimsWorktreesCard
                .cardAppear(index: 2)

            gatewayDaemonCard
                .cardAppear(index: 3)

            sandboxCard
                .cardAppear(index: 4)

            Text("Presence remains read-only in mobile.")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textTertiary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task {
            await viewModel.loadSectionIfNeeded(.runtime)
        }
        .confirmationDialog("Start Sandbox?", isPresented: $showSandboxStartConfirmation, titleVisibility: .visible) {
            Button("Start Sandbox") {
                Task {
                    await viewModel.startSandbox(
                        project: startSandboxProject,
                        agentID: startSandboxAgentID
                    )
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This triggers sandbox start/build for the selected project.")
        }
    }

    // MARK: - Spawn Launcher

    private var spawnLauncherCard: some View {
        NavigationLink {
            SpawnAgentView(viewModel: SpawnViewModel(apiClient: viewModel.apiClient), broadcaster: broadcaster)
        } label: {
            LoomCard {
                HStack {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Launch Runtime")
                            .font(LoomTypography.headlineMedium)
                            .foregroundStyle(LoomColors.textPrimary)
                        Text("Spawn a headless agent or warm a sandbox when you need to create capacity.")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                    Spacer()
                    Image(systemName: "play.circle.fill")
                        .font(.title2)
                        .foregroundStyle(LoomColors.accent)
                }
            }
        }
    }

    // MARK: - Presence Card

    private var presenceCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Presence Summary")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                Text("Use runtime status to spot whether the fleet is available before opening the deeper People views.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)

                #if canImport(Charts)
                FleetCompositionChart(
                    active: viewModel.presenceSummary.activeAgents,
                    idle: viewModel.presenceSummary.idleAgents,
                    offline: viewModel.presenceSummary.offlineAgents
                )
                #endif

                HStack {
                    opsMetric(label: "Active", value: viewModel.presenceSummary.activeAgents, icon: "bolt.fill", color: LoomColors.statusHealthy)
                    Spacer()
                    opsMetric(label: "Idle", value: viewModel.presenceSummary.idleAgents, icon: "moon.fill", color: LoomColors.statusIdle)
                    Spacer()
                    opsMetric(label: "Offline", value: viewModel.presenceSummary.offlineAgents, icon: "xmark.circle.fill", color: LoomColors.statusCritical)
                }

                if viewModel.presenceAgents.isEmpty {
                    Text("No agents")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textTertiary)
                } else {
                    ForEach(Array(viewModel.presenceAgents.prefix(agentDisplayLimit))) { agent in
                        HStack(spacing: LoomSpacing.sm) {
                            ZStack {
                                Image(systemName: LoomColors.agentTypeIcon(agent.agentType))
                                    .font(.caption)
                                    .foregroundStyle(LoomColors.agentTypeColor(agent.agentType))
                                    .frame(width: 20, height: 20)
                                PulsingDot(color: agentStatusColor(agent.status), isPulsing: agent.status.rawValue == "active")
                                    .offset(x: 8, y: 8)
                            }
                            .frame(width: 24, height: 24)
                            VStack(alignment: .leading, spacing: 2) {
                                HStack(spacing: 6) {
                                    Text(agent.agentId)
                                        .font(LoomTypography.bodyMedium)
                                        .foregroundStyle(LoomColors.agentTypeColor(agent.agentType))
                                        .lineLimit(1)
                                    StatusBadge(
                                        agent.status.rawValue,
                                        color: agentStatusColor(agent.status)
                                    )
                                }
                                if !agent.currentTask.isEmpty {
                                    Text(agent.currentTask)
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.textSecondary)
                                        .lineLimit(1)
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .padding(.vertical, 2)
                    }
                    if viewModel.presenceAgents.count > agentDisplayLimit {
                        Button {
                            withAnimation(.easeInOut(duration: 0.25)) {
                                agentDisplayLimit += 8
                            }
                            HapticManager.light()
                        } label: {
                            Text("Show \(min(8, viewModel.presenceAgents.count - agentDisplayLimit)) More")
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.accent)
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 6)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Claims & Worktrees Card

    private var claimsWorktreesCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Claims & Worktrees")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                HStack(spacing: LoomSpacing.lg) {
                    HStack(spacing: LoomSpacing.xs) {
                        Image(systemName: "lock.fill")
                            .foregroundStyle(LoomColors.accent)
                        Text("Claims:")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textSecondary)
                        AnimatedCounter(viewModel.presenceClaims.count)
                            .font(LoomTypography.counterSmall)
                    }
                    HStack(spacing: LoomSpacing.xs) {
                        Image(systemName: "arrow.triangle.branch")
                            .foregroundStyle(LoomColors.accent)
                        Text("Worktrees:")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textSecondary)
                        AnimatedCounter(viewModel.presenceWorktrees.count)
                            .font(LoomTypography.counterSmall)
                    }
                }

                if let topology = viewModel.topology {
                    HStack(spacing: LoomSpacing.xs) {
                        Image(systemName: "point.3.connected.trianglepath.dotted")
                            .foregroundStyle(LoomColors.statusInfo)
                        Text("Topology: \(topology.nodes.count) nodes \u{2022} \(topology.edges.count) edges")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                } else {
                    Text("Topology unavailable")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    // MARK: - Gateway & Daemon Card

    private var gatewayDaemonCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Gateway & Daemon")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                if let controlPlane = viewModel.controlPlane {
                    HStack {
                        opsMetric(label: "Servers", value: controlPlane.health.totalServers, icon: "server.rack", color: LoomColors.accent)
                        Spacer()
                        opsMetric(label: "Hub", value: controlPlane.health.hubTargets, icon: "globe", color: LoomColors.statusInfo)
                        Spacer()
                        opsMetric(label: "Local", value: controlPlane.health.localTargets, icon: "desktopcomputer", color: LoomColors.statusActive)
                        Spacer()
                        opsMetric(label: "Idle", value: controlPlane.health.idleServers, icon: "moon.fill", color: LoomColors.statusIdle)
                    }

                    VStack(alignment: .leading, spacing: 4) {
                        Label("Health: \(controlPlane.health.healthyServers) healthy \u{2022} \(controlPlane.health.degradedServers) degraded \u{2022} \(controlPlane.health.downServers) down", systemImage: "heart.fill")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)

                        Label("RBAC: \(controlPlane.rbac.enabled ? "on" : "off") \u{2022} roles \(controlPlane.rbac.roleCount) \u{2022} bindings \(controlPlane.rbac.bindingCount) \u{2022} denied \(controlPlane.rbac.deniedCount)", systemImage: "shield.fill")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)

                        Label("OTel: \(controlPlane.otel.otlpConfigured ? "configured" : "off") \u{2022} traced \(controlPlane.otel.tracedServers)/\(controlPlane.otel.totalServers)", systemImage: "waveform.path")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)

                        Label("Cost: \(controlPlane.cost.totalCalls) calls \u{2022} errors \(controlPlane.cost.totalErrors) \u{2022} denied \(controlPlane.cost.totalDenied)", systemImage: "dollarsign.circle")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                } else {
                    Text("Control-plane telemetry unavailable")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    // MARK: - Sandbox Card

    private var sandboxCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Sandbox / Devbox")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                Text("Scoped mobile mutations: sandbox start/stop only.")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)

                TextField("Project (e.g. loom-core)", text: $startSandboxProject)
                    .textFieldStyle(.roundedBorder)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif

                TextField("Agent ID (optional)", text: $startSandboxAgentID)
                    .textFieldStyle(.roundedBorder)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif

                Button {
                    viewModel.clearMutationMessages()
                    showSandboxStartConfirmation = true
                } label: {
                    if viewModel.isMutatingSandbox {
                        ProgressView()
                            .frame(maxWidth: .infinity)
                    } else {
                        Label("Start Sandbox", systemImage: "play.circle")
                            .frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(
                    startSandboxProject.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
                    viewModel.isMutatingSandbox
                )

                Divider()

                if let sandbox = viewModel.sandboxSummary {
                    if sandbox.available {
                        HStack {
                            opsMetric(label: "Running", value: sandbox.totalRunning, icon: "play.circle.fill", color: LoomColors.statusHealthy)
                            Spacer()
                            VStack(alignment: .leading, spacing: 2) {
                                Text(sandbox.backend)
                                    .font(LoomTypography.counterSmall)
                                    .foregroundStyle(LoomColors.textPrimary)
                                Text("Backend")
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textSecondary)
                            }
                        }

                        if sandbox.projects.isEmpty {
                            Text("No active sandboxes")
                                .font(LoomTypography.bodyRegular)
                                .foregroundStyle(LoomColors.textTertiary)
                        } else {
                            ForEach(sandbox.projects) { project in
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(project.project)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                        Text("\(project.status) \u{2022} \(project.agentId) \u{2022} \(project.uptime)")
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                    }
                                    Spacer()
                                    Button(role: .destructive) {
                                        Task { await viewModel.stopSandbox(project: project.project) }
                                    } label: {
                                        Image(systemName: "stop.circle")
                                    }
                                    .buttonStyle(.borderless)
                                    .disabled(viewModel.isMutatingSandbox)
                                }
                                .padding(.vertical, 2)
                            }
                        }
                    } else {
                        Text("Devbox unavailable")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                } else {
                    Text("Sandbox data unavailable")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textTertiary)
                }

                if let msg = viewModel.sandboxMutationMessage {
                    Text(msg)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                }
                if let err = viewModel.sandboxMutationError {
                    Text(err)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.statusCritical)
                }
            }
        }
    }

    // MARK: - Helpers

    private func opsMetric(label: String, value: Int, icon: String, color: Color) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.caption2)
                    .foregroundStyle(color)
                AnimatedCounter(value)
                    .font(LoomTypography.counterSmall)
                    .foregroundStyle(LoomColors.textPrimary)
            }
            Text(label)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        }
    }

    private func agentStatusColor(_ status: MobilePresenceStatus) -> Color {
        switch status {
        case .active: return LoomColors.statusHealthy
        case .idle: return LoomColors.statusIdle
        case .offline: return LoomColors.statusCritical
        case .unknown: return LoomColors.statusIdle
        }
    }
}
