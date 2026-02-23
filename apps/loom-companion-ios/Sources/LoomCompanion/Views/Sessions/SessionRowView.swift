import SwiftUI
import LoomCompanionKit

struct SessionRowView: View {
    let session: SessionInfo

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(session.agentId)
                    .font(.subheadline)
                    .fontWeight(.medium)
                Spacer()
                StatusBadge(sessionStatus: session.status)
            }

            Text(session.namespace)
                .font(.caption)
                .foregroundStyle(.secondary)

            if !session.description.isEmpty {
                Text(session.description)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            HStack(spacing: 12) {
                Label("\(session.entryCount)", systemImage: "doc.text")
                Label("\(session.totalTokens)", systemImage: "number")
                Spacer()
                Text(session.startedAt)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 4)
    }
}
