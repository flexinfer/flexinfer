import SwiftUI
import LoomCompanionKit

/// View for spawning headless AI coding agents and monitoring active spawns.
struct SpawnAgentView: View {
    @State var viewModel: SpawnViewModel
    var broadcaster: SSEEventBroadcaster?

    @State private var selectedAgentType: AgentType = .claudeCode
    @State private var project = ""
    @State private var branch = ""
    @State private var taskDescription = ""
    @State private var showingConfirmation = false

    var body: some View {
        List {
            spawnFormSection
            activeSpawnsSection
        }
        .navigationTitle("Spawn Agent")
        .task {
            async let config: () = viewModel.loadConfig()
            async let spawns: () = viewModel.loadSpawns()
            _ = await (config, spawns)
            if let broadcaster {
                viewModel.startListening(broadcaster: broadcaster)
            }
        }
        .refreshable {
            await viewModel.loadSpawns()
        }
        .alert("Spawn Agent?", isPresented: $showingConfirmation) {
            Button("Spawn", role: .destructive) {
                Task { await spawn() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This will start a \(selectedAgentType.displayName) agent in the K8s cluster to work on: \(taskDescription)")
        }
    }

    // MARK: - Form Section

    private var spawnFormSection: some View {
        Section("New Agent") {
            // Agent type picker — shows only available agents when config loaded.
            Picker("Agent Type", selection: $selectedAgentType) {
                if let config = viewModel.config {
                    ForEach(availableAgentTypes(from: config)) { info in
                        Text(info.name).tag(agentTypeFromID(info.id))
                    }
                } else {
                    ForEach(AgentType.allCases) { type in
                        Text(type.displayName).tag(type)
                    }
                }
            }

            // Project picker — populated from config endpoint.
            if let config = viewModel.config, !config.projects.isEmpty {
                Picker("Project", selection: $project) {
                    Text("Select project").tag("")
                    ForEach(config.projects) { proj in
                        Text(proj.name).tag(proj.name)
                    }
                }
            } else {
                TextField("Project", text: $project)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }

            TextField("Branch (optional)", text: $branch)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()

            TextField("Task Description", text: $taskDescription, axis: .vertical)
                .lineLimit(3...6)

            Button {
                showingConfirmation = true
            } label: {
                if viewModel.isSpawning {
                    ProgressView()
                        .frame(maxWidth: .infinity)
                } else {
                    Text("Spawn Agent")
                        .frame(maxWidth: .infinity)
                }
            }
            .buttonStyle(.borderedProminent)
            .disabled(!isFormValid || viewModel.isSpawning)
        }
    }

    // MARK: - Active Spawns

    private var activeSpawnsSection: some View {
        Section("Active Spawns") {
            if viewModel.spawns.isEmpty {
                Text("No active spawns")
                    .foregroundStyle(.secondary)
            } else {
                TimelineView(.periodic(from: .now, by: 1)) { _ in
                    ForEach(viewModel.spawns) { spawn in
                        SpawnRow(spawn: spawn) {
                            Task { await viewModel.stopSpawn(id: spawn.spawnId) }
                        }
                    }
                }
            }
        }
    }

    // MARK: - Helpers

    private var isFormValid: Bool {
        !project.isEmpty && !taskDescription.isEmpty
    }

    private func spawn() async {
        _ = await viewModel.spawnAgent(
            agentType: selectedAgentType,
            project: project,
            branch: branch,
            taskDescription: taskDescription
        )
        // Clear form on success.
        if viewModel.error == nil {
            taskDescription = ""
        }
    }

    private func availableAgentTypes(from config: SpawnConfig) -> [SpawnAgentTypeInfo] {
        config.agentTypes.filter(\.available)
    }

    private func agentTypeFromID(_ id: String) -> AgentType {
        AgentType(rawValue: id) ?? .claudeCode
    }
}

/// Row displaying a single spawn's status.
private struct SpawnRow: View {
    let spawn: MobileSpawnStatus
    let onStop: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(spawn.request.project)
                    .font(.headline)
                Spacer()
                StatusBadge(spawn.status, color: spawnStatusColor(spawn.status))
            }

            Text(spawn.request.taskDescription)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(2)

            HStack {
                Text(spawn.agentId)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                Spacer()
                if spawn.isActive, let elapsed = elapsedString {
                    Text(elapsed)
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                        .monospacedDigit()
                }
                if spawn.isActive {
                    Button("Stop", role: .destructive, action: onStop)
                        .font(.caption)
                        .buttonStyle(.bordered)
                }
            }
        }
        .padding(.vertical, 4)
    }

    private var elapsedString: String? {
        guard let date = ISO8601DateFormatter().date(from: spawn.startedAt) else { return nil }
        let elapsed = Date().timeIntervalSince(date)
        let minutes = Int(elapsed) / 60
        let seconds = Int(elapsed) % 60
        return String(format: "%d:%02d", minutes, seconds)
    }
}

private func spawnStatusColor(_ status: String) -> Color {
    switch status {
    case "running": return .green
    case "creating": return .blue
    case "completed": return .secondary
    case "failed": return .red
    case "stopped": return .orange
    default: return .secondary
    }
}
