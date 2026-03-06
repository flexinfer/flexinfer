import SwiftUI
import LoomCompanionKit

struct OpsTaskDetailView: View {
    let task: MobileTask

    var body: some View {
        List {
            Section("Overview") {
                LabeledContent("Title", value: task.title)
                LabeledContent("Status") {
                    StatusBadge(task.status.rawValue, color: statusColor)
                }
                LabeledContent("Priority", value: task.priority)
                LabeledContent("Agent", value: task.agentId)
                if !task.namespace.isEmpty {
                    LabeledContent("Namespace", value: task.namespace)
                }
                if !task.sessionId.isEmpty {
                    LabeledContent("Session", value: task.sessionId)
                }
            }

            if !task.context.isEmpty {
                Section("Context") {
                    Text(task.context)
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textPrimary)
                        .textSelection(.enabled)
                }
            }

            if !task.tags.isEmpty {
                Section("Tags") {
                    FlowLayout(spacing: LoomSpacing.xs) {
                        ForEach(task.tags, id: \.self) { tag in
                            Text(tag)
                                .font(LoomTypography.caption)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 4)
                                .background(LoomColors.accent.opacity(0.12), in: Capsule())
                                .foregroundStyle(LoomColors.accent)
                        }
                    }
                }
            }

            if !task.blockedBy.isEmpty {
                Section("Blocked By") {
                    ForEach(task.blockedBy, id: \.self) { blockerId in
                        Label(blockerId, systemImage: "exclamationmark.triangle")
                            .font(LoomTypography.bodyRegular)
                            .foregroundStyle(LoomColors.statusDegraded)
                    }
                }
            }

            Section("Timestamps") {
                if !task.createdAt.isEmpty {
                    LabeledContent("Created", value: task.createdAt)
                }
                if !task.updatedAt.isEmpty {
                    LabeledContent("Updated", value: task.updatedAt)
                }
            }
        }
        .navigationTitle("Task")
        .navigationBarTitleDisplayMode(.inline)
    }

    private var statusColor: Color {
        switch task.status {
        case .pending: return LoomColors.statusIdle
        case .inProgress: return LoomColors.statusActive
        case .blocked: return LoomColors.statusDegraded
        case .completed: return LoomColors.statusHealthy
        case .unknown: return LoomColors.statusIdle
        }
    }
}

private struct FlowLayout: Layout {
    var spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let result = layout(proposal: proposal, subviews: subviews)
        return result.size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = layout(proposal: proposal, subviews: subviews)
        for (index, position) in result.positions.enumerated() {
            subviews[index].place(at: CGPoint(x: bounds.minX + position.x, y: bounds.minY + position.y), proposal: .unspecified)
        }
    }

    private func layout(proposal: ProposedViewSize, subviews: Subviews) -> (size: CGSize, positions: [CGPoint]) {
        let maxWidth = proposal.width ?? .infinity
        var positions: [CGPoint] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0
        var totalHeight: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x + size.width > maxWidth && x > 0 {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            positions.append(CGPoint(x: x, y: y))
            rowHeight = max(rowHeight, size.height)
            x += size.width + spacing
        }
        totalHeight = y + rowHeight

        return (CGSize(width: maxWidth, height: totalHeight), positions)
    }
}
