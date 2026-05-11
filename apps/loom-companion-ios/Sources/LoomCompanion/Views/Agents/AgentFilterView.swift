import SwiftUI
import LoomCompanionKit

struct AgentFilterView: View {
    @Binding var statusFilter: MobilePresenceStatus?
    @Binding var attentionOnly: Bool
    var summary: UnifiedAgentsSummary?
    var pipelineAgentCount: Int = 0
    var attentionCount: Int = 0

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
        AgentBadgeFlow(spacing: LoomSpacing.sm) {
            if attentionCount > 0 {
                attentionToggle
            }
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

    private var attentionToggle: some View {
        Button {
            HapticManager.light()
            attentionOnly.toggle()
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.caption2)
                Text("Attention \(attentionCount)")
                    .font(.caption2)
                    .fontWeight(.semibold)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(
                (attentionOnly ? LoomColors.statusCritical : LoomColors.statusCritical.opacity(0.15))
            )
            .foregroundStyle(attentionOnly ? Color.white : LoomColors.statusCritical)
            .clipShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(attentionOnly ? "Showing only agents needing attention" : "Filter to agents needing attention")
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

private struct AgentBadgeFlow: Layout {
    let spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? 0
        var rowWidth: CGFloat = 0
        var rowHeight: CGFloat = 0
        var totalWidth: CGFloat = 0
        var totalHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            let nextWidth = rowWidth == 0 ? size.width : rowWidth + spacing + size.width
            if maxWidth > 0, nextWidth > maxWidth {
                totalWidth = max(totalWidth, rowWidth)
                totalHeight += rowHeight + spacing
                rowWidth = size.width
                rowHeight = size.height
            } else {
                rowWidth = nextWidth
                rowHeight = max(rowHeight, size.height)
            }
        }

        totalWidth = max(totalWidth, rowWidth)
        totalHeight += rowHeight
        return CGSize(width: maxWidth > 0 ? maxWidth : totalWidth, height: totalHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX
        var y = bounds.minY
        var rowHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > bounds.minX, x + size.width > bounds.maxX {
                x = bounds.minX
                y += rowHeight + spacing
                rowHeight = 0
            }
            subview.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
    }
}
