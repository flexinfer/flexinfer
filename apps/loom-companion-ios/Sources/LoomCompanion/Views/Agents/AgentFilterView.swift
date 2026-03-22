import SwiftUI
import LoomCompanionKit

struct AgentFilterView: View {
    @Binding var statusFilter: MobilePresenceStatus?
    var summary: UnifiedAgentsSummary?
    var pipelineAgentCount: Int = 0

    var body: some View {
        VStack(spacing: LoomSpacing.sm) {
            if let summary {
                summaryBadges(summary)
            }

            Picker("Status", selection: $statusFilter) {
                Text("All").tag(MobilePresenceStatus?.none)
                Text("Active").tag(MobilePresenceStatus?.some(.active))
                Text("Idle").tag(MobilePresenceStatus?.some(.idle))
                Text("Offline").tag(MobilePresenceStatus?.some(.offline))
            }
            .pickerStyle(.segmented)
        }
        .listRowInsets(EdgeInsets(top: 8, leading: 16, bottom: 8, trailing: 16))
        .listRowBackground(Color.clear)
    }

    @ViewBuilder
    private func summaryBadges(_ summary: UnifiedAgentsSummary) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: LoomSpacing.sm) {
                summaryBadge("Active", count: summary.activeAgents, color: LoomColors.statusHealthy)
                summaryBadge("Idle", count: summary.idleAgents, color: .orange)
                summaryBadge("Offline", count: summary.offlineAgents, color: LoomColors.statusIdle)
                if summary.spawnedAgents > 0 {
                    summaryBadge("K8s", count: summary.spawnedAgents, color: .purple)
                }
                if pipelineAgentCount > 0 {
                    summaryBadge("CI", count: pipelineAgentCount, color: LoomColors.statusActive)
                }
            }
        }
    }

    private func summaryBadge(_ label: String, count: Int, color: Color) -> some View {
        HStack(spacing: 4) {
            Circle()
                .fill(color)
                .frame(width: 8, height: 8)
            Text("\(label) \(count)")
                .font(.caption2)
                .fontWeight(.medium)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(color.opacity(0.1))
        .clipShape(Capsule())
    }
}
