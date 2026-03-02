import AppIntents

struct LoomShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(
            intent: CheckFleetHealthIntent(),
            phrases: [
                "How's my \(.applicationName) fleet?",
                "Check \(.applicationName) health",
                "\(.applicationName) status",
            ],
            shortTitle: "Fleet Health",
            systemImageName: "heart.fill"
        )

        AppShortcut(
            intent: ListBlockedTasksIntent(),
            phrases: [
                "Show blocked \(.applicationName) tasks",
                "Any blocked tasks in \(.applicationName)?",
            ],
            shortTitle: "Blocked Tasks",
            systemImageName: "exclamationmark.triangle"
        )

        AppShortcut(
            intent: GetActiveSessionsIntent(),
            phrases: [
                "Active \(.applicationName) sessions",
                "How many agents are running in \(.applicationName)?",
            ],
            shortTitle: "Active Sessions",
            systemImageName: "person.2.fill"
        )
    }
}
