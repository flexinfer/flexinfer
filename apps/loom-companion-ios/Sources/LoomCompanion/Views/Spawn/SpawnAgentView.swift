import SwiftUI
import LoomCompanionKit

/// View for spawning headless AI coding agents and monitoring active spawns.
struct SpawnAgentView: View {
    @State var viewModel: SpawnViewModel

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
            await viewModel.loadSpawns()
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
            Picker("Agent Type", selection: $selectedAgentType) {
                ForEach(AgentType.allCases) { type in
                    Text(type.displayName).tag(type)
                }
            }

            TextField("Project", text: $project)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()

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
                ForEach(viewModel.spawns) { spawn in
                    SpawnRow(spawn: spawn) {
                        Task { await viewModel.stopSpawn(id: spawn.spawnId) }
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
                if spawn.isActive {
                    Button("Stop", role: .destructive, action: onStop)
                        .font(.caption)
                        .buttonStyle(.bordered)
                }
            }
        }
        .padding(.vertical, 4)
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
