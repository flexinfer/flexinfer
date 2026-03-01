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
    @State private var showCreateConfirmation = false
    @State private var showEndConfirmation = false

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
                    ProgressView("Loading Ops data...")
                        .frame(maxWidth: .infinity, alignment: .leading)
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
        VStack(spacing: 12) {
            GroupBox("Tasks") {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        metric(label: "Pending", value: "\(viewModel.taskCounts.pending)")
                        Spacer()
                        metric(label: "In Progress", value: "\(viewModel.taskCounts.inProgress)")
                        Spacer()
                        metric(label: "Blocked", value: "\(viewModel.taskCounts.blocked)")
                        Spacer()
                        metric(label: "Completed", value: "\(viewModel.taskCounts.completed)")
                    }
                    .font(.caption)

                    if viewModel.tasks.isEmpty {
                        Text("No tasks")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(Array(viewModel.tasks.prefix(8))) { task in
                            NavigationLink {
                                OpsTaskDetailView(task: task)
                            } label: {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(task.title).font(.subheadline).fontWeight(.medium)
                                    Text("\(task.agentId) • \(task.status.rawValue)")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
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

            GroupBox("Workflows") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Pending approvals: \(viewModel.pendingApprovals)")
                        .font(.subheadline)
                    if viewModel.workflows.isEmpty {
                        Text("No workflows")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(Array(viewModel.workflows.prefix(8))) { workflow in
                            NavigationLink {
                                OpsWorkflowDetailView(
                                    workflow: workflow,
                                    loadDetail: viewModel.loadWorkflowDetail(id:)
                                )
                            } label: {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(workflow.name ?? workflow.id)
                                        .font(.subheadline)
                                        .fontWeight(.medium)
                                    Text("\(workflow.status.rawValue) • \(workflow.currentStep ?? "No current step")")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.vertical, 2)
                            }
                        }
                    }
                }
            }

            GroupBox("Session Actions") {
                VStack(alignment: .leading, spacing: 10) {
                    Text("Scoped mobile mutations: session create/end only.")
                        .font(.caption)
                        .foregroundStyle(.secondary)

                    Text("Start Session")
                        .font(.subheadline)
                        .fontWeight(.semibold)
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
                        .font(.subheadline)
                        .fontWeight(.semibold)
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

            Text("Mutations require proper mobile scopes (`mobile:session:create`, `mobile:session:end`).")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var agentsSection: some View {
        VStack(spacing: 12) {
            GroupBox("Presence Summary") {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        metric(label: "Active", value: "\(viewModel.presenceSummary.activeAgents)")
                        Spacer()
                        metric(label: "Idle", value: "\(viewModel.presenceSummary.idleAgents)")
                        Spacer()
                        metric(label: "Offline", value: "\(viewModel.presenceSummary.offlineAgents)")
                    }
                    .font(.caption)

                    if viewModel.presenceAgents.isEmpty {
                        Text("No agents")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(Array(viewModel.presenceAgents.prefix(8))) { agent in
                            VStack(alignment: .leading, spacing: 2) {
                                Text(agent.agentId).font(.subheadline).fontWeight(.medium)
                                Text("\(agent.status.rawValue) • \(agent.currentTask)")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, 2)
                        }
                    }
                }
            }

            GroupBox("Claims & Worktrees") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Claims: \(viewModel.presenceClaims.count) • Worktrees: \(viewModel.presenceWorktrees.count)")
                        .font(.subheadline)
                    if let topology = viewModel.topology {
                        Text("Topology: \(topology.nodes.count) nodes • \(topology.edges.count) edges")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Topology unavailable")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            GroupBox("Gateway & Daemon") {
                VStack(alignment: .leading, spacing: 8) {
                    if let controlPlane = viewModel.controlPlane {
                        HStack {
                            metric(label: "Servers", value: "\(controlPlane.health.totalServers)")
                            Spacer()
                            metric(label: "Hub", value: "\(controlPlane.health.hubTargets)")
                            Spacer()
                            metric(label: "Local", value: "\(controlPlane.health.localTargets)")
                            Spacer()
                            metric(label: "Idle", value: "\(controlPlane.health.idleServers)")
                        }
                        .font(.caption)

                        Text("Health: \(controlPlane.health.healthyServers) healthy • \(controlPlane.health.degradedServers) degraded • \(controlPlane.health.downServers) down")
                            .font(.caption)
                            .foregroundStyle(.secondary)

                        Text("RBAC: \(controlPlane.rbac.enabled ? "on" : "off") • roles \(controlPlane.rbac.roleCount) • bindings \(controlPlane.rbac.bindingCount) • denied \(controlPlane.rbac.deniedCount)")
                            .font(.caption)
                            .foregroundStyle(.secondary)

                        Text("OTel: \(controlPlane.otel.otlpConfigured ? "configured" : "off") • traced \(controlPlane.otel.tracedServers)/\(controlPlane.otel.totalServers)")
                            .font(.caption)
                            .foregroundStyle(.secondary)

                        Text("Cost: \(controlPlane.cost.totalCalls) calls • errors \(controlPlane.cost.totalErrors) • denied \(controlPlane.cost.totalDenied)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Control-plane telemetry unavailable")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            GroupBox("Sandbox / Devbox") {
                VStack(alignment: .leading, spacing: 8) {
                    if let sandbox = viewModel.sandboxSummary {
                        if sandbox.available {
                            HStack {
                                metric(label: "Running", value: "\(sandbox.totalRunning)")
                                Spacer()
                                metric(label: "Backend", value: sandbox.backend)
                            }
                            .font(.caption)

                            if sandbox.projects.isEmpty {
                                Text("No active sandboxes")
                                    .foregroundStyle(.secondary)
                            } else {
                                ForEach(sandbox.projects) { project in
                                    HStack {
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(project.project).font(.subheadline).fontWeight(.medium)
                                            Text("\(project.status) • \(project.agentId) • \(project.uptime)")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
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
                                .foregroundStyle(.secondary)
                        }
                    } else {
                        Text("Sandbox data unavailable")
                            .foregroundStyle(.secondary)
                    }

                    if let msg = viewModel.sandboxMutationMessage {
                        Text(msg)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    if let err = viewModel.sandboxMutationError {
                        Text(err)
                            .font(.caption)
                            .foregroundStyle(.red)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            Text("Read-only in Wave 1. Presence operations stay in HUD/TUI for now.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var knowledgeSection: some View {
        VStack(spacing: 12) {
            GroupBox("Memory") {
                VStack(alignment: .leading, spacing: 8) {
                    if let stats = viewModel.memoryStats {
                        Text("Items: \(stats.totalItems) • Tokens: \(stats.totalTokens)")
                            .font(.subheadline)
                        Text("Working \(stats.workingMemory.items) • Short \(stats.shortTermMemory.items) • Long \(stats.longTermMemory.items)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Memory stats unavailable")
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            GroupBox("Stream") {
                VStack(alignment: .leading, spacing: 8) {
                    if viewModel.streamEntries.isEmpty {
                        Text("No stream entries")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(Array(viewModel.streamEntries.prefix(8))) { entry in
                            VStack(alignment: .leading, spacing: 2) {
                                Text(entry.title).font(.subheadline).fontWeight(.medium)
                                Text("\(entry.entryType) • \(entry.agentId)")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, 2)
                        }
                    }
                }
            }

            GroupBox("Graph & Reasoning") {
                VStack(alignment: .leading, spacing: 8) {
                    if let stats = viewModel.graphStats {
                        Text("Graph: \(stats.totalEntities) entities • \(stats.totalRelations) relations")
                            .font(.subheadline)
                    }
                    if let path = viewModel.graphPath {
                        Text("Sample path length: \(path.length)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    if viewModel.reasoningChains.isEmpty {
                        Text("Reasoning chains: 0")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(Array(viewModel.reasoningChains.prefix(8))) { chain in
                            NavigationLink {
                                OpsReasoningChainDetailView(
                                    chain: chain,
                                    loadDetail: viewModel.loadReasoningChainDetail(id:)
                                )
                            } label: {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(chain.title)
                                        .font(.subheadline)
                                        .fontWeight(.medium)
                                    Text("\(chain.status.rawValue) • \(chain.stepCount) steps")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.vertical, 2)
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            Text("Read-only in Wave 1. Editing/promoting graph/memory is deferred.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private func metric(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(value).fontWeight(.semibold)
            Text(label).foregroundStyle(.secondary)
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

private struct OpsTaskDetailView: View {
    let task: MobileTask

    var body: some View {
        List {
            Section("Task") {
                Text(task.title)
                Text("Status: \(task.status.rawValue)")
                Text("Priority: \(task.priority)")
                Text("Agent: \(task.agentId)")
                Text("Session: \(task.sessionId)")
                Text("Namespace: \(task.namespace)")
            }

            Section("Context") {
                if task.context.isEmpty {
                    Text("No context provided")
                        .foregroundStyle(.secondary)
                } else {
                    Text(task.context)
                }
            }

            Section("Metadata") {
                Text("Created: \(task.createdAt)")
                Text("Updated: \(task.updatedAt)")
                Text("Tags: \(task.tags.isEmpty ? "none" : task.tags.joined(separator: ", "))")
                Text("Blocked by: \(task.blockedBy.isEmpty ? "none" : task.blockedBy.joined(separator: ", "))")
            }
        }
        .navigationTitle("Task")
    }
}

private struct OpsWorkflowDetailView: View {
    let workflow: MobileWorkflow
    let loadDetail: (String) async throws -> MobileWorkflowDetailResponse

    @State private var detail: MobileWorkflowDetailResponse?
    @State private var errorText: String?
    @State private var isLoading = false

    var body: some View {
        List {
            Section("Workflow") {
                Text(workflow.name ?? workflow.id)
                Text("Status: \(workflow.status.rawValue)")
                Text("Progress: \(Int((workflow.progress * 100).rounded()))%")
                Text("Current step: \(workflow.currentStep ?? "None")")
            }

            if let detail {
                Section("Steps") {
                    if detail.workflow.steps.isEmpty {
                        Text("No steps")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(detail.workflow.steps) { step in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(step.name).font(.subheadline).fontWeight(.medium)
                                Text("\(step.status.rawValue) • \(step.type ?? "tool")")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                if let error = step.error, !error.isEmpty {
                                    Text(error)
                                        .font(.caption)
                                        .foregroundStyle(.red)
                                }
                            }
                        }
                    }
                }

                Section("Events") {
                    if detail.events.isEmpty {
                        Text("No events")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(detail.events) { event in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(event.eventType).font(.subheadline).fontWeight(.medium)
                                Text(event.timestamp)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                if let stepName = event.stepName, !stepName.isEmpty {
                                    Text(stepName)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                }
            } else if let errorText {
                Section {
                    Text(errorText).foregroundStyle(.red)
                }
            } else if isLoading {
                Section {
                    ProgressView("Loading workflow detail...")
                }
            }
        }
        .navigationTitle("Workflow")
        .task {
            guard detail == nil, errorText == nil else { return }
            isLoading = true
            defer { isLoading = false }
            do {
                detail = try await loadDetail(workflow.id)
            } catch {
                errorText = error.localizedDescription
            }
        }
    }
}

private struct OpsReasoningChainDetailView: View {
    let chain: MobileReasoningChain
    let loadDetail: (String) async throws -> MobileReasoningChainDetailResponse

    @State private var detail: MobileReasoningChainDetailResponse?
    @State private var errorText: String?
    @State private var isLoading = false

    var body: some View {
        List {
            Section("Chain") {
                Text(chain.title)
                Text("Status: \(chain.status.rawValue)")
                Text("Steps: \(chain.stepCount)")
            }

            if let detail {
                Section("Reasoning Steps") {
                    if detail.chain.steps?.isEmpty != false {
                        Text("No steps")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(detail.chain.steps ?? []) { step in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(step.description).font(.subheadline)
                                Text("Confidence: \(Int((step.confidence * 100).rounded()))%")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                Text(step.createdAt)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            } else if let errorText {
                Section {
                    Text(errorText).foregroundStyle(.red)
                }
            } else if isLoading {
                Section {
                    ProgressView("Loading reasoning detail...")
                }
            }
        }
        .navigationTitle("Reasoning")
        .task {
            guard detail == nil, errorText == nil else { return }
            isLoading = true
            defer { isLoading = false }
            do {
                detail = try await loadDetail(chain.id)
            } catch {
                errorText = error.localizedDescription
            }
        }
    }
}
