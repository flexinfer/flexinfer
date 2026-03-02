import AppIntents
import LoomCompanionKit

struct ListBlockedTasksIntent: AppIntent {
    static var title: LocalizedStringResource = "List Blocked Tasks"
    static var description: IntentDescription = "Show currently blocked tasks in your Loom fleet."
    static var openAppWhenRun = false

    func perform() async throws -> some IntentResult & ProvidesDialog {
        guard let data = SharedDataStore.load() else {
            return .result(dialog: "Unable to load task data. Open the Loom app to refresh.")
        }

        let tasks = data.tasks
        if tasks.blocked == 0 {
            return .result(dialog: "No blocked tasks. \(tasks.pending) pending, \(tasks.inProgress) in progress, \(tasks.completed) completed.")
        }

        var summary = "\(tasks.blocked) blocked task\(tasks.blocked == 1 ? "" : "s")."
        summary += " Also: \(tasks.pending) pending, \(tasks.inProgress) in progress."

        return .result(dialog: "\(summary)")
    }
}
