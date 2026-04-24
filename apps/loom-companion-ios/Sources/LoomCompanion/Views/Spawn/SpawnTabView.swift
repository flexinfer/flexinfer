import SwiftUI
import LoomCompanionKit

/// Root view for the Spawn tab. The fast path is: pick a preset (or reuse
/// last-used defaults) → type the task → tap Spawn. Agent type / project /
/// branch are hidden behind a "More options" disclosure so the common
/// repeat-spawn flow stays one-tap.
struct SpawnTabView: View {
    let apiClient: APIClient?
    var broadcaster: SSEEventBroadcaster?

    @State private var viewModel: SpawnViewModel

    // Form state — seeded from SpawnPreferences on first render so repeat
    // spawns are frictionless.
    @State private var agentType: AgentType
    @State private var project: String
    @State private var branch: String
    @State private var taskDescription: String = ""
    @State private var selectedPresetID: String?
    @State private var showMoreOptions = false
    @State private var showConfirmation = false

    @State private var templates: [SpawnTemplate] = []
    @State private var showSaveTemplateSheet = false
    @State private var templateBeingRenamed: SpawnTemplate?
    @State private var renameDraft: String = ""

    init(apiClient: APIClient?, broadcaster: SSEEventBroadcaster? = nil) {
        self.apiClient = apiClient
        self.broadcaster = broadcaster
        let client = apiClient ?? APIClient(baseURL: URL(string: "http://localhost")!, token: "mock-token")
        _viewModel = State(initialValue: SpawnViewModel(apiClient: client))

        let prefs = SpawnPreferences.load()
        _agentType = State(initialValue: prefs.agentType)
        _project = State(initialValue: prefs.project)
        _branch = State(initialValue: prefs.branch)
        _templates = State(initialValue: SpawnPreferences.loadTemplates())
    }

    var body: some View {
        List {
            quickSpawnSection
            if !templates.isEmpty {
                templatesSection
            }
            presetsSection
            moreOptionsSection
            activeSpawnsSection
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Spawn")
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
        .alert("Spawn Agent?", isPresented: $showConfirmation) {
            Button("Spawn", role: .destructive) {
                Task { await submitSpawn() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Launch \(agentType.displayName) on \(project.isEmpty ? "(no project)" : project)\(branch.isEmpty ? "" : " @ \(branch)") to work on:\n\n\(taskDescription)")
        }
        .sheet(isPresented: $showSaveTemplateSheet) {
            SaveTemplateSheet(
                agentType: agentType,
                project: project,
                branch: branch,
                taskDescription: taskDescription,
                onSave: { name in
                    addTemplate(name: name)
                    showSaveTemplateSheet = false
                },
                onCancel: { showSaveTemplateSheet = false }
            )
        }
        .sheet(item: $templateBeingRenamed) { template in
            RenameTemplateSheet(
                initialName: template.name,
                onSave: { newName in
                    renameTemplate(template.id, to: newName)
                    templateBeingRenamed = nil
                },
                onCancel: { templateBeingRenamed = nil }
            )
        }
    }

    // MARK: - Quick Spawn

    private var quickSpawnSection: some View {
        Section {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                quickSpawnHeaderChips

                TextField("What should the agent do?", text: $taskDescription, axis: .vertical)
                    .lineLimit(3...8)
                    .font(LoomTypography.bodyRegular)

                Button {
                    showConfirmation = true
                } label: {
                    if viewModel.isSpawning {
                        ProgressView().frame(maxWidth: .infinity)
                    } else {
                        Label("Spawn Agent", systemImage: "sparkles")
                            .frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!isFormValid || viewModel.isSpawning)

                Button {
                    showSaveTemplateSheet = true
                } label: {
                    Label("Save as template…", systemImage: "bookmark")
                        .font(LoomTypography.caption)
                }
                .buttonStyle(.plain)
                .foregroundStyle(LoomColors.accent)
                .disabled(!canSaveTemplate)

                if let hint = disabledHint {
                    Text(hint)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
            .padding(.vertical, 4)
        } header: {
            Text("Quick Spawn")
        }
    }

    private var canSaveTemplate: Bool {
        !project.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private var quickSpawnHeaderChips: some View {
        HStack(spacing: LoomSpacing.xs) {
            chip(icon: "cpu", label: agentType.displayName)
            chip(icon: "folder", label: project.isEmpty ? "No project" : project)
            if !branch.isEmpty {
                chip(icon: "arrow.triangle.branch", label: branch)
            }
            Spacer()
            Button {
                withAnimation { showMoreOptions.toggle() }
            } label: {
                Image(systemName: showMoreOptions ? "chevron.up" : "slider.horizontal.3")
                    .font(.caption)
                    .foregroundStyle(LoomColors.accent)
            }
            .accessibilityLabel("Toggle more options")
        }
    }

    private func chip(icon: String, label: String) -> some View {
        HStack(spacing: 4) {
            Image(systemName: icon)
                .font(.caption2)
                .foregroundStyle(LoomColors.accent)
            Text(label)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
                .lineLimit(1)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(LoomColors.bgElevated, in: Capsule())
    }

    // MARK: - Presets

    private var presetsSection: some View {
        Section {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: LoomSpacing.xs) {
                    ForEach(SpawnPreset.builtins) { preset in
                        presetChip(preset)
                    }
                }
                .padding(.vertical, 2)
            }
            .listRowInsets(EdgeInsets(top: 4, leading: 16, bottom: 4, trailing: 16))
        } header: {
            Text("Presets")
        } footer: {
            Text("Tap a preset to fill the task field, then edit before spawning.")
                .font(LoomTypography.caption)
        }
    }

    private func presetChip(_ preset: SpawnPreset) -> some View {
        Button {
            applyPreset(preset)
        } label: {
            HStack(spacing: 6) {
                Image(systemName: preset.icon)
                    .font(.caption)
                Text(preset.label)
                    .font(LoomTypography.bodyMedium)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(
                selectedPresetID == preset.id
                    ? LoomColors.accent.opacity(0.18)
                    : LoomColors.bgElevated,
                in: Capsule()
            )
            .foregroundStyle(
                selectedPresetID == preset.id
                    ? LoomColors.accent
                    : LoomColors.textPrimary
            )
        }
        .buttonStyle(.plain)
    }

    // MARK: - Templates

    private var templatesSection: some View {
        Section {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: LoomSpacing.xs) {
                    ForEach(templates) { template in
                        templateChip(template)
                    }
                }
                .padding(.vertical, 2)
            }
            .listRowInsets(EdgeInsets(top: 4, leading: 16, bottom: 4, trailing: 16))
        } header: {
            HStack {
                Text("Saved templates")
                Spacer()
                Text("\(templates.count)")
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textTertiary)
            }
        } footer: {
            Text("Tap a saved template to load its agent, project, branch, and task. Long-press to rename or delete.")
                .font(LoomTypography.caption)
        }
    }

    private func templateChip(_ template: SpawnTemplate) -> some View {
        Button {
            applyTemplate(template)
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "bookmark.fill")
                    .font(.caption)
                Text(template.name)
                    .font(LoomTypography.bodyMedium)
                    .lineLimit(1)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(LoomColors.bgElevated, in: Capsule())
            .foregroundStyle(LoomColors.textPrimary)
        }
        .buttonStyle(.plain)
        .contextMenu {
            Button {
                templateBeingRenamed = template
            } label: {
                Label("Rename", systemImage: "pencil")
            }
            Button(role: .destructive) {
                deleteTemplate(template.id)
            } label: {
                Label("Delete", systemImage: "trash")
            }
        }
    }

    private func applyTemplate(_ template: SpawnTemplate) {
        agentType = template.agentType
        project = template.project
        branch = template.branch
        taskDescription = template.taskTemplate
        selectedPresetID = nil
    }

    private func addTemplate(name: String) {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        let template = SpawnTemplate(
            name: trimmed,
            agentType: agentType,
            project: project,
            branch: branch,
            taskTemplate: taskDescription
        )
        templates.append(template)
        SpawnPreferences.saveTemplates(templates)
    }

    private func deleteTemplate(_ id: UUID) {
        templates.removeAll { $0.id == id }
        SpawnPreferences.saveTemplates(templates)
    }

    private func renameTemplate(_ id: UUID, to newName: String) {
        let trimmed = newName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        guard let idx = templates.firstIndex(where: { $0.id == id }) else { return }
        templates[idx].name = trimmed
        SpawnPreferences.saveTemplates(templates)
    }

    // MARK: - More Options

    @ViewBuilder
    private var moreOptionsSection: some View {
        if showMoreOptions {
            Section {
                Picker("Agent Type", selection: $agentType) {
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

                if let projects = uniqueProjects, !projects.isEmpty {
                    Picker("Project", selection: $project) {
                        Text("Select project").tag("")
                        ForEach(Array(projects.enumerated()), id: \.offset) { _, proj in
                            Text(proj.name).tag(proj.name)
                        }
                    }
                } else {
                    TextField("Project", text: $project)
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        #endif
                        .autocorrectionDisabled()
                }

                TextField("Branch (optional)", text: $branch)
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
                    .autocorrectionDisabled()
            } header: {
                Text("More options")
            }
        }
    }

    private var uniqueProjects: [SpawnProjectInfo]? {
        guard let projects = viewModel.config?.projects else { return nil }
        var seen = Set<String>()
        return projects.filter { seen.insert($0.name).inserted }
    }

    private func availableAgentTypes(from config: SpawnConfig) -> [SpawnAgentTypeInfo] {
        config.agentTypes.filter(\.available)
    }

    private func agentTypeFromID(_ id: String) -> AgentType {
        AgentType(rawValue: id) ?? .claudeCode
    }

    // MARK: - Active Spawns

    private var activeSpawnsSection: some View {
        Section {
            if viewModel.spawns.isEmpty {
                Text("No active spawns")
                    .foregroundStyle(LoomColors.textTertiary)
            } else {
                TimelineView(.periodic(from: .now, by: 1)) { _ in
                    ForEach(viewModel.spawns) { spawn in
                        NavigationLink {
                            SpawnDetailView(
                                spawn: spawn,
                                viewModel: viewModel,
                                apiClient: viewModel.apiClient
                            )
                        } label: {
                            SpawnRowView(spawn: spawn) {
                                Task { await viewModel.stopSpawn(id: spawn.spawnId) }
                            }
                        }
                    }
                }
            }
        } header: {
            HStack {
                Text("Active Spawns")
                Spacer()
                if !viewModel.spawns.isEmpty {
                    Text("\(viewModel.spawns.count)")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    // MARK: - Actions

    private func applyPreset(_ preset: SpawnPreset) {
        selectedPresetID = preset.id
        // Only overwrite if user hasn't typed their own task, so tapping a
        // preset after edits doesn't clobber ongoing work.
        if taskDescription.isEmpty || taskIsFromPreset() {
            taskDescription = preset.taskTemplate
        }
        if agentType == .claudeCode && preset.suggestedAgentType != .claudeCode {
            agentType = preset.suggestedAgentType
        }
    }

    private func taskIsFromPreset() -> Bool {
        SpawnPreset.builtins.contains { $0.taskTemplate == taskDescription }
    }

    private var isFormValid: Bool {
        !project.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !taskDescription.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private var disabledHint: String? {
        if viewModel.isSpawning { return nil }
        if project.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return "Pick a project in More options before spawning."
        }
        if taskDescription.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return "Describe what the agent should do."
        }
        return nil
    }

    private func submitSpawn() async {
        _ = await viewModel.spawnAgent(
            agentType: agentType,
            project: project,
            branch: branch,
            taskDescription: taskDescription
        )
        if viewModel.error == nil {
            SpawnPreferences.save(
                SpawnPreferences.Snapshot(agentType: agentType, project: project, branch: branch)
            )
            taskDescription = ""
            selectedPresetID = nil
        }
    }
}

// Row displaying a single spawn's status. Replicated from the old
// SpawnAgentView so the new Spawn tab body is self-contained.
private struct SpawnRowView: View {
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
                .foregroundStyle(LoomColors.textSecondary)
                .lineLimit(2)

            HStack {
                Text(spawn.agentId)
                    .font(.caption2)
                    .foregroundStyle(LoomColors.textTertiary)
                Spacer()
                if spawn.isActive, let elapsed = elapsedString {
                    Text(elapsed)
                        .font(.caption2)
                        .foregroundStyle(LoomColors.textTertiary)
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
    case "running": return LoomColors.statusHealthy
    case "creating": return LoomColors.info
    case "completed": return LoomColors.textTertiary
    case "failed": return LoomColors.statusCritical
    case "stopped": return LoomColors.statusDegraded
    default: return LoomColors.textTertiary
    }
}

// MARK: - Save Template Sheet

private struct SaveTemplateSheet: View {
    let agentType: AgentType
    let project: String
    let branch: String
    let taskDescription: String
    let onSave: (String) -> Void
    let onCancel: () -> Void

    @State private var name: String = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Template name") {
                    TextField("e.g. Fix CI failures on main", text: $name)
                        #if os(iOS)
                        .textInputAutocapitalization(.sentences)
                        #endif
                }
                Section("Captures") {
                    LabeledContent("Agent", value: agentType.displayName)
                    LabeledContent("Project", value: project.isEmpty ? "—" : project)
                    if !branch.isEmpty {
                        LabeledContent("Branch", value: branch)
                    }
                    if !taskDescription.isEmpty {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Task")
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.textTertiary)
                            Text(taskDescription)
                                .font(LoomTypography.bodyRegular)
                                .lineLimit(6)
                        }
                    }
                }
            }
            .navigationTitle("Save template")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onCancel)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { onSave(name) }
                        .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }
}

// MARK: - Rename Template Sheet

private struct RenameTemplateSheet: View {
    let initialName: String
    let onSave: (String) -> Void
    let onCancel: () -> Void

    @State private var name: String

    init(initialName: String, onSave: @escaping (String) -> Void, onCancel: @escaping () -> Void) {
        self.initialName = initialName
        self.onSave = onSave
        self.onCancel = onCancel
        _name = State(initialValue: initialName)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Template name") {
                    TextField("Name", text: $name)
                        #if os(iOS)
                        .textInputAutocapitalization(.sentences)
                        #endif
                }
            }
            .navigationTitle("Rename")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onCancel)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { onSave(name) }
                        .disabled(
                            name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
                            name == initialName
                        )
                }
            }
        }
    }
}
