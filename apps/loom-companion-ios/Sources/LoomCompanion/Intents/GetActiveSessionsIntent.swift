import AppIntents
import LoomCompanionKit

struct GetActiveSessionsIntent: AppIntent {
    static var title: LocalizedStringResource = "Get Active Sessions"
    static var description: IntentDescription = "Check how many agent sessions are currently active."
    static var openAppWhenRun = false

    func perform() async throws -> some IntentResult & ProvidesDialog {
        guard let data = SharedDataStore.load() else {
            return .result(dialog: "Unable to load session data. Open the Loom app to refresh.")
        }

        let sessions = data.sessions
        if sessions.activeCount == 0 {
            return .result(dialog: "No active sessions right now.")
        }

        var summary = "\(sessions.activeCount) active session\(sessions.activeCount == 1 ? "" : "s")."

        for session in sessions.topSessions.prefix(3) {
            summary += " \(session.agentId) on \(session.namespace)."
        }

        return .result(dialog: "\(summary)")
    }
}
