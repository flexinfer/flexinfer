import SwiftUI
import LoomCompanionKit

struct HealthStatusCard: View {
    let health: HealthSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Server Health")
                    .font(.headline)
                Spacer()
                StatusBadge(healthStatus: health.overallStatus)
            }

            HStack(spacing: 20) {
                HealthMetric(
                    label: "Healthy",
                    count: health.healthyServers,
                    color: .green
                )
                HealthMetric(
                    label: "Degraded",
                    count: health.degradedServers,
                    color: .orange
                )
                HealthMetric(
                    label: "Down",
                    count: health.downServers,
                    color: .red
                )
                HealthMetric(
                    label: "Idle",
                    count: health.idleServers,
                    color: .secondary
                )
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

private struct HealthMetric: View {
    let label: String
    let count: Int
    let color: Color

    var body: some View {
        VStack(spacing: 4) {
            Text("\(count)")
                .font(.title2)
                .fontWeight(.semibold)
                .foregroundStyle(color)
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}
