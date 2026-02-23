import SwiftUI
import LoomCompanionKit

struct FleetSummaryCard: View {
    let dashboard: DashboardData

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Fleet Overview")
                    .font(.headline)
                Spacer()
                HStack(spacing: 4) {
                    Circle()
                        .fill(dashboard.daemonRunning ? .green : .red)
                        .frame(width: 8, height: 8)
                    Text(dashboard.daemonRunning ? "Daemon running" : "Daemon stopped")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            LazyVGrid(columns: [
                GridItem(.flexible()),
                GridItem(.flexible()),
                GridItem(.flexible()),
            ], spacing: 12) {
                SummaryTile(
                    label: "Sessions",
                    value: "\(dashboard.activeSessions)",
                    icon: "rectangle.stack"
                )
                SummaryTile(
                    label: "Active",
                    value: "\(dashboard.activeAgents)",
                    icon: "person.fill"
                )
                SummaryTile(
                    label: "Idle",
                    value: "\(dashboard.idleAgents)",
                    icon: "person"
                )
                SummaryTile(
                    label: "Offline",
                    value: "\(dashboard.offlineAgents)",
                    icon: "person.slash"
                )
                SummaryTile(
                    label: "Servers",
                    value: "\(dashboard.serverCount)",
                    icon: "server.rack"
                )
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

private struct SummaryTile: View {
    let label: String
    let value: String
    let icon: String

    var body: some View {
        VStack(spacing: 6) {
            Image(systemName: icon)
                .font(.title3)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title3)
                .fontWeight(.semibold)
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 8)
    }
}
