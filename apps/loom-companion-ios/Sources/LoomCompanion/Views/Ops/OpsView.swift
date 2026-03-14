import SwiftUI
import LoomCompanionKit

struct OpsView: View {
    @State private var viewModel: OpsViewModel
    @State private var selectedSegment: OpsSegment = .work
    @Binding private var deepLinkWorkflowID: String?
    @Binding private var prefillEndSessionID: String?
    @State private var deepLinkedWorkflow: MobileWorkflow?
    @State private var pendingDeepLinkWorkflowID: String?
    @State private var toastMessage: String?
    @State private var showToast = false
    @State private var createAgentID = ""
    @State private var createNamespace = ""
    @State private var createDescription = ""
    @State private var createAutoRecall = true
    @State private var endSessionID = ""
    @State private var endWithSummary = false
    @State private var startSandboxProject = ""
    @State private var startSandboxAgentID = ""
    @State private var showCreateConfirmation = false
    @State private var showEndConfirmation = false
    @State private var showSandboxStartConfirmation = false

    enum OpsSegment: String, CaseIterable, Identifiable {
        case work = "Work"
        case agents = "Agents"
        case knowledge = "Knowledge"

        var id: String { rawValue }
    }

    init(
        apiClient: APIClient?,
        deepLinkWorkflowID: Binding<String?> = .constant(nil),
        prefillEndSessionID: Binding<String?> = .constant(nil)
    ) {
        let client = apiClient ?? APIClient(baseURL: URL(string: "http://localhost")!, token: "mock-token")
        _deepLinkWorkflowID = deepLinkWorkflowID
        _prefillEndSessionID = prefillEndSessionID
        _viewModel = State(initialValue: OpsViewModel(apiClient: client))
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                Picker("Ops Segment", selection: $selectedSegment) {
                    ForEach(OpsSegment.allCases) { segment in
                        Text(segment.rawValue).tag(segment)
                    }
                }
                .pickerStyle(.segmented)

                if let error = viewModel.error {
                    Text(error.description)
                        .font(.subheadline)
                        .foregroundStyle(.red)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if let warningMessage = viewModel.warningMessage {
                    Text(warningMessage)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if let statusMessage = viewModel.mutationStatusMessage {
                    Text(statusMessage)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                if let mutationErrorMessage = viewModel.mutationErrorMessage {
                    Text(mutationErrorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }

                if viewModel.isLoading {
                    VStack(spacing: LoomSpacing.cardSpacing) {
                        SkeletonDashboardCard()
                            .cardAppear(index: 0)
                        SkeletonDashboardCard()
                            .cardAppear(index: 1)
                        SkeletonDashboardCard()
                            .cardAppear(index: 2)
                    }
                }

                switch selectedSegment {
                case .work:
                    workSection
                case .agents:
                    agentsSection
                case .knowledge:
                    knowledgeSection
                }
            }
            .padding()
        }
        .navigationTitle("Ops")
        .task {
            await viewModel.load()
            resolveDeepLinkWorkflow()
        }
        .refreshable {
            await viewModel.load()
            resolveDeepLinkWorkflow()
            HapticManager.light()
        }
        .onChange(of: deepLinkWorkflowID) { _, newValue in
            pendingDeepLinkWorkflowID = newValue
            resolveDeepLinkWorkflow()
        }
        .onChange(of: viewModel.workflows.map(\.id)) { _, _ in
            resolveDeepLinkWorkflow()
        }
        .onChange(of: prefillEndSessionID) { _, newValue in
            guard let newValue else { return }
            prefillEndSession(with: newValue)
            prefillEndSessionID = nil
        }
        .sheet(item: $deepLinkedWorkflow) { workflow in
            NavigationStack {
                OpsWorkflowDetailView(
                    workflow: workflow,
                    loadDetail: viewModel.loadWorkflowDetail(id:)
                )
            }
        }
        .confirmationDialog("Start Session?", isPresented: $showCreateConfirmation, titleVisibility: .visible) {
            Button("Start Session") {
                Task {
                    await viewModel.createSession(
                        agentID: createAgentID,
                        namespace: createNamespace,
                        description: createDescription,
                        autoRecall: createAutoRecall
                    )
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This creates a new agent-context session from mobile.")
        }
        .confirmationDialog("End Session?", isPresented: $showEndConfirmation, titleVisibility: .visible) {
            Button("End Session", role: .destructive) {
                Task { await viewModel.endSession(sessionID: endSessionID, summarize: endWithSummary) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(endWithSummary ? "This will end the session and request a summary." : "This will end the session without summary.")
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
        .overlay(alignment: .top) {
            if showToast, let toastMessage {
                Text(toastMessage)
                    .font(.caption)
                    .foregroundStyle(.white)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Color.black.opacity(0.85))
                    .clipShape(Capsule())
                    .padding(.top, 8)
                    .transition(.opacity)
            }
        }
    }

    private var workSection: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                    Text("Tasks")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    #if canImport(Charts)
                    TaskStatusChart(
                        pending: viewModel.taskCounts.pending,
                        inProgress: viewModel.taskCounts.inProgress,
                        blocked: viewModel.taskCounts.blocked,
                        completed: viewModel.taskCounts.completed
                    )
                    #endif

                    HStack {
                        opsMetric(label: "Pending", value: viewModel.taskCounts.pending, icon: "clock", color: LoomColors.statusIdle)
                        Spacer()
                        opsMetric(label: "Active", value: viewModel.taskCounts.inProgress, icon: "bolt.fill", color: LoomColors.statusActive)
                        Spacer()
                        opsMetric(label: "Blocked", value: viewModel.taskCounts.blocked, icon: "exclamationmark.triangle.fill", color: LoomColors.statusBlocked)
                        Spacer()
                        opsMetric(label: "Done", value: viewModel.taskCounts.completed, icon: "checkmark.circle.fill", color: LoomColors.statusHealthy)
                    }

                    if viewModel.tasks.isEmpty {
                        Text("No tasks")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                    } else {
                        ForEach(Array(viewModel.tasks.prefix(8))) { task in
                            NavigationLink {
                                OpsTaskDetailView(task: task)
                            } label: {
                                HStack(spacing: LoomSpacing.sm) {
                                    StatusAccentBar(color: taskStatusColor(task.status))
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(task.title)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                        Text("\(task.agentId) \u{2022} \(task.status.rawValue)")
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .padding(.vertical, 2)
                            }
                            .contextMenu {
                                Button {
                                    prefillEndSession(with: task.sessionId)
                                } label: {
                                    Label("Prefill End Session", systemImage: "arrowshape.turn.up.left")
                                }
                                .disabled(task.sessionId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                            }
                        }
                    }
                }
            }
            .cardAppear(index: 0)

            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                    Text("Workflows")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    HStack {
                        Text("Pending approvals:")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textSecondary)
                        AnimatedCounter(viewModel.pendingApprovals)
                            .font(LoomTypography.counterMedium)
                            .foregroundStyle(viewModel.pendingApprovals > 0 ? LoomColors.statusDegraded : LoomColors.textPrimary)
                    }

                    if viewModel.workflows.isEmpty {
                        Text("No workflows")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                    } else {
                        ForEach(Array(viewModel.workflows.prefix(8))) { workflow in
                            NavigationLink {
                                OpsWorkflowDetailView(
                                    workflow: workflow,
                                    loadDetail: viewModel.loadWorkflowDetail(id:)
                                )
                            } label: {
                                HStack(spacing: LoomSpacing.sm) {
                                    StatusAccentBar(color: workflowStatusColor(workflow.status))
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(workflow.name ?? workflow.id)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                        Text("\(workflow.status.rawValue) \u{2022} \(workflow.currentStep ?? "No current step")")
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .padding(.vertical, 2)
                            }
                        }
                    }
                }
            }
            .cardAppear(index: 1)

            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.md) {
                    Text("Session Actions")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    Text("Scoped mobile mutations: session create/end only.")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)

                    Text("Start Session")
                        .font(LoomTypography.bodyMedium)
                    TextField("Agent ID", text: $createAgentID)
                        .textFieldStyle(.roundedBorder)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        #endif
                    TextField("Namespace (optional)", text: $createNamespace)
                        .textFieldStyle(.roundedBorder)
                        .autocorrectionDisabled()
                    TextField("Description (optional)", text: $createDescription)
                        .textFieldStyle(.roundedBorder)
                    Toggle("Auto recall", isOn: $createAutoRecall)

                    Button {
                        viewModel.clearMutationMessages()
                        showCreateConfirmation = true
                    } label: {
                        if viewModel.isMutatingSession {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                        } else {
                            Text("Start Session")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(createAgentID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || viewModel.isMutatingSession)

                    Divider()

                    Text("End Session")
                        .font(LoomTypography.bodyMedium)
                    TextField("Session ID", text: $endSessionID)
                        .textFieldStyle(.roundedBorder)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        #endif
                    Toggle("Include summary", isOn: $endWithSummary)

                    Button(role: .destructive) {
                        viewModel.clearMutationMessages()
                        showEndConfirmation = true
                    } label: {
                        if viewModel.isMutatingSession {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                        } else {
                            Text("End Session")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .buttonStyle(.bordered)
                    .disabled(endSessionID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || viewModel.isMutatingSession)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            Text("Mutations require proper mobile scopes.")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textTertiary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var agentsSection: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            NavigationLink {
                SpawnAgentView(viewModel: SpawnViewModel(apiClient: viewModel.apiClient))
            } label: {
                LoomCard {
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Spawn Agent")
                                .font(LoomTypography.headlineMedium)
                                .foregroundStyle(LoomColors.textPrimary)
                            Text("Launch a headless AI agent in K8s")
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
            .cardAppear(index: 0)

            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                    Text("Presence Summary")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

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
                        ForEach(Array(viewModel.presenceAgents.prefix(8))) { agent in
                            HStack(spacing: LoomSpacing.sm) {
                                PulsingDot(color: agentStatusColor(agent.status), isPulsing: agent.status.rawValue == "active")
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(agent.agentId)
                                        .font(LoomTypography.bodyMedium)
                                        .foregroundStyle(LoomColors.textPrimary)
                                    Text("\(agent.status.rawValue) \u{2022} \(agent.currentTask)")
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.textSecondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                            }
                            .padding(.vertical, 2)
                        }
                    }
                }
            }
            .cardAppear(index: 0)

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
            .cardAppear(index: 1)

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
            .cardAppear(index: 2)

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
                            .foregroundStyle(.red)
                    }
                }
            }
            .cardAppear(index: 3)

            Text("Presence remains read-only in mobile.")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textTertiary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var knowledgeSection: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                    Text("Memory")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    if let stats = viewModel.memoryStats {
                        #if canImport(Charts)
                        MemoryTierChart(stats: stats)
                        #endif

                        HStack {
                            opsMetric(label: "Items", value: stats.totalItems, icon: "brain.head.profile.fill", color: LoomColors.accent)
                            Spacer()
                            opsMetric(label: "Tokens", value: stats.totalTokens, icon: "text.word.spacing", color: LoomColors.statusInfo)
                        }

                        HStack(spacing: LoomSpacing.lg) {
                            Label("Working \(stats.workingMemory.items)", systemImage: "bolt.fill")
                            Label("Short \(stats.shortTermMemory.items)", systemImage: "clock.fill")
                            Label("Long \(stats.longTermMemory.items)", systemImage: "archivebox.fill")
                        }
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                    } else {
                        Text("Memory stats unavailable")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                }
            }
            .cardAppear(index: 0)

            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                    Text("Stream")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    if viewModel.streamEntries.isEmpty {
                        Text("No stream entries")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.textTertiary)
                    } else {
                        ForEach(Array(viewModel.streamEntries.prefix(8).enumerated()), id: \.element.id) { index, entry in
                            HStack(spacing: LoomSpacing.sm) {
                                Image(systemName: streamEntryIcon(entry.entryType))
                                    .foregroundStyle(LoomColors.accent)
                                    .symbolEffect(.bounce, value: entry.id)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(entry.title)
                                        .font(LoomTypography.bodyMedium)
                                        .foregroundStyle(LoomColors.textPrimary)
                                    Text("\(entry.entryType) \u{2022} \(entry.agentId)")
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.textSecondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                            }
                            .padding(.vertical, 2)
                            .cardAppear(index: index)
                        }
                    }
                }
            }
            .cardAppear(index: 1)

            LoomCard {
                VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                    Text("Graph & Reasoning")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.textPrimary)

                    if let stats = viewModel.graphStats {
                        HStack {
                            opsMetric(label: "Entities", value: stats.totalEntities, icon: "circle.hexagongrid.fill", color: LoomColors.accent)
                            Spacer()
                            opsMetric(label: "Relations", value: stats.totalRelations, icon: "arrow.triangle.branch", color: LoomColors.statusInfo)
                        }
                    }
                    if let path = viewModel.graphPath {
                        Label("Sample path length: \(path.length)", systemImage: "point.3.connected.trianglepath.dotted")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                    }
                    if viewModel.reasoningChains.isEmpty {
                        Text("Reasoning chains: 0")
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textTertiary)
                    } else {
                        ForEach(Array(viewModel.reasoningChains.prefix(8))) { chain in
                            NavigationLink {
                                OpsReasoningChainDetailView(
                                    chain: chain,
                                    loadDetail: viewModel.loadReasoningChainDetail(id:)
                                )
                            } label: {
                                HStack(spacing: LoomSpacing.sm) {
                                    StatusAccentBar(color: chainStatusColor(chain.status))
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(chain.title)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                        Text("\(chain.status.rawValue) \u{2022} \(chain.stepCount) steps")
                                            .font(LoomTypography.caption)
                                            .foregroundStyle(LoomColors.textSecondary)
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .padding(.vertical, 2)
                            }
                        }
                    }
                }
            }
            .cardAppear(index: 2)

            Text("Read-only in Wave 1.")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textTertiary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

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

    private func taskStatusColor(_ status: MobileTaskStatus) -> Color {
        switch status {
        case .pending: return LoomColors.statusIdle
        case .inProgress: return LoomColors.statusActive
        case .blocked: return LoomColors.statusBlocked
        case .completed: return LoomColors.statusHealthy
        case .unknown: return LoomColors.statusIdle
        }
    }

    private func workflowStatusColor(_ status: MobileWorkflowStatus) -> Color {
        switch status {
        case .running: return LoomColors.statusActive
        case .completed: return LoomColors.statusHealthy
        case .failed: return LoomColors.statusCritical
        case .waitingApproval: return LoomColors.statusDegraded
        case .cancelled: return LoomColors.statusIdle
        case .unknown: return LoomColors.statusIdle
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

    private func chainStatusColor(_ status: MobileReasoningStatus) -> Color {
        switch status {
        case .active: return LoomColors.statusActive
        case .completed: return LoomColors.statusHealthy
        case .abandoned: return LoomColors.statusCritical
        case .unknown: return LoomColors.statusIdle
        }
    }

    private func streamEntryIcon(_ entryType: String) -> String {
        switch entryType {
        case "decision": return "lightbulb.fill"
        case "observation": return "eye.fill"
        case "progress": return "arrow.right.circle.fill"
        case "error": return "exclamationmark.triangle.fill"
        case "context": return "doc.text.fill"
        default: return "circle.fill"
        }
    }

    private func resolveDeepLinkWorkflow() {
        let requested = pendingDeepLinkWorkflowID ?? deepLinkWorkflowID
        guard let workflowID = requested?.trimmingCharacters(in: .whitespacesAndNewlines),
              !workflowID.isEmpty
        else {
            return
        }

        if let workflow = viewModel.workflows.first(where: { $0.id == workflowID }) {
            selectedSegment = .work
            deepLinkedWorkflow = workflow
            pendingDeepLinkWorkflowID = nil
            deepLinkWorkflowID = nil
            return
        }

        guard !viewModel.isLoading else { return }

        pendingDeepLinkWorkflowID = nil
        deepLinkWorkflowID = nil
        showToastMessage("Workflow \(workflowID) is not in the current list")
    }

    private func prefillEndSession(with sessionID: String) {
        let trimmed = sessionID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        selectedSegment = .work
        endSessionID = trimmed
        showToastMessage("End Session prefilled: \(trimmed)")
    }

    private func showToastMessage(_ message: String) {
        toastMessage = message
        withAnimation {
            showToast = true
        }
        Task {
            try? await Task.sleep(for: .seconds(2.5))
            await MainActor.run {
                withAnimation {
                    showToast = false
                }
            }
        }
    }
}
