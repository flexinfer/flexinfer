import Foundation

/// Persists the last-used spawn parameters so Quick Spawn can re-apply them on
/// the next app launch. Stored in the App Group UserDefaults so the extension
/// side can read them too (not used yet, but future-proof for a widget-driven
/// spawn in a later phase).
public enum SpawnPreferences {
    public static let appGroupID = "group.ai.flexinfer.loom.companion"

    private enum Keys {
        static let lastAgentType = "spawn.lastAgentType"
        static let lastProject = "spawn.lastProject"
        static let lastBranch = "spawn.lastBranch"
    }

    public struct Snapshot: Equatable, Sendable {
        public var agentType: AgentType
        public var project: String
        public var branch: String

        public init(agentType: AgentType = .claudeCode, project: String = "", branch: String = "") {
            self.agentType = agentType
            self.project = project
            self.branch = branch
        }
    }

    /// Read last-used params, falling back to sensible defaults.
    public static func load() -> Snapshot {
        guard let defaults = UserDefaults(suiteName: appGroupID) else {
            return Snapshot()
        }
        let agentRaw = defaults.string(forKey: Keys.lastAgentType) ?? ""
        let agent = AgentType(rawValue: agentRaw) ?? .claudeCode
        return Snapshot(
            agentType: agent,
            project: defaults.string(forKey: Keys.lastProject) ?? "",
            branch: defaults.string(forKey: Keys.lastBranch) ?? ""
        )
    }

    /// Write after a successful spawn so the next visit pre-fills the form.
    public static func save(_ snapshot: Snapshot) {
        guard let defaults = UserDefaults(suiteName: appGroupID) else { return }
        defaults.set(snapshot.agentType.rawValue, forKey: Keys.lastAgentType)
        defaults.set(snapshot.project, forKey: Keys.lastProject)
        defaults.set(snapshot.branch, forKey: Keys.lastBranch)
    }
}

/// Built-in task templates for one-tap spawn kickoff. Users see these as
/// chips on the Spawn tab; tapping one fills the Task field with the template
/// so they can edit before submitting.
public struct SpawnPreset: Identifiable, Hashable, Sendable {
    public let id: String
    public let label: String
    public let icon: String
    public let taskTemplate: String
    public let suggestedAgentType: AgentType

    public init(id: String, label: String, icon: String, taskTemplate: String, suggestedAgentType: AgentType = .claudeCode) {
        self.id = id
        self.label = label
        self.icon = icon
        self.taskTemplate = taskTemplate
        self.suggestedAgentType = suggestedAgentType
    }

    public static let builtins: [SpawnPreset] = [
        SpawnPreset(
            id: "fix-bug",
            label: "Fix bug",
            icon: "ant.fill",
            taskTemplate: "Investigate and fix the bug described below. Reproduce first, add a regression test, then land the smallest fix.\n\nBug:\n- "
        ),
        SpawnPreset(
            id: "add-feature",
            label: "Add feature",
            icon: "sparkles",
            taskTemplate: "Implement the feature described below. Start by proposing a plan, then ship it in focused slices with tests.\n\nFeature:\n- "
        ),
        SpawnPreset(
            id: "investigate-ci",
            label: "Investigate CI",
            icon: "stethoscope",
            taskTemplate: "The CI pipeline is failing. Diagnose the failure, classify it (flake / real bug / infra), and land the smallest fix that gets the pipeline green.\n\nPipeline URL or failure context:\n- "
        ),
        SpawnPreset(
            id: "refactor",
            label: "Refactor",
            icon: "arrow.triangle.2.circlepath",
            taskTemplate: "Refactor the target code for clarity and testability without changing behavior. Keep the diff small and verify tests stay green.\n\nTarget:\n- "
        ),
    ]
}
