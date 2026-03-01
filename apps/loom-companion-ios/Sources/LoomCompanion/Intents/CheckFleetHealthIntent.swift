import AppIntents
import LoomCompanionKit

struct CheckFleetHealthIntent: AppIntent {
    static var title: LocalizedStringResource = "Check Fleet Health"
    static var description: IntentDescription = "Check the current health status of your Loom fleet."
    static var openAppWhenRun = false

    func perform() async throws -> some IntentResult & ProvidesDialog {
        guard let data = SharedDataStore.load() else {
            return .result(dialog: "Unable to load fleet data. Open the Loom app to refresh.")
        }

        let fleet = data.fleet
        let status = fleet.daemonRunning ? "running" : "stopped"
        let healthy = fleet.healthyServers
        let total = fleet.healthyServers + fleet.degradedServers + fleet.downServers

        var summary = "Daemon is \(status). \(healthy)/\(total) servers healthy."

        if fleet.degradedServers > 0 {
            summary += " \(fleet.degradedServers) degraded."
        }
        if fleet.downServers > 0 {
            summary += " \(fleet.downServers) down."
        }

        summary += " \(fleet.activeAgents) active agents, \(fleet.sessionCount) sessions."

        return .result(dialog: "\(summary)")
    }
}
