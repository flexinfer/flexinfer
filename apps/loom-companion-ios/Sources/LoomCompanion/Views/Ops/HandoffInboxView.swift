import SwiftUI
import LoomCompanionKit

/// Read-only list of pending handoffs: source agent, target, description, timestamp.
struct HandoffInboxView: View {
    let handoffs: [MobileHandoff]

    var body: some View {
        if handoffs.isEmpty {
            ContentUnavailableView {
                Label("No Handoffs", systemImage: "arrow.left.arrow.right")
            } description: {
                Text("Pending agent handoffs will appear here.")
            }
        } else {
            LazyVStack(spacing: 12) {
                ForEach(handoffs) { handoff in
                    HandoffCard(handoff: handoff)
                        .cardAppear(index: handoffs.firstIndex(where: { $0.id == handoff.id }) ?? 0)
                }
            }
        }
    }
}

private struct HandoffCard: View {
    let handoff: MobileHandoff

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "arrow.right.circle.fill")
                    .foregroundStyle(LoomColors.statusDegraded)
                VStack(alignment: .leading, spacing: 2) {
                    Text(handoff.fromAgent)
                        .font(.subheadline)
                        .fontWeight(.medium)
                    Text("to \(handoff.toAgent)")
                        .font(.caption)
                        .foregroundStyle(LoomColors.fgSecondary)
                }
                Spacer()
                statusBadge(handoff.status)
            }

            if !handoff.summary.isEmpty {
                Text(handoff.summary)
                    .font(.caption)
                    .foregroundStyle(LoomColors.fgPrimary)
                    .lineLimit(3)
            }

            HStack {
                Text(handoff.createdAt)
                    .font(.caption2)
                    .foregroundStyle(LoomColors.fgMuted)
                Spacer()
            }
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(.background)
                .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
        )
    }

    @ViewBuilder
    private func statusBadge(_ status: String) -> some View {
        Text(status.replacingOccurrences(of: "_", with: " ").capitalized)
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(statusColor(status).opacity(0.15))
            .foregroundStyle(statusColor(status))
            .clipShape(Capsule())
    }

    private func statusColor(_ status: String) -> Color {
        switch status {
        case "pending": return LoomColors.statusDegraded
        case "accepted": return LoomColors.statusHealthy
        case "rejected": return LoomColors.statusCritical
        case "viewed": return LoomColors.info
        default: return LoomColors.fgMuted
        }
    }
}
