import SwiftUI
import LoomCompanionKit

struct SessionMetadataView: View {
    let session: SessionInfo

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Session Info")
                    .font(.headline)
                Spacer()
                StatusBadge(sessionStatus: session.status)
            }

            Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("ID").foregroundStyle(.secondary)
                    Text(session.id).font(.caption).monospaced()
                }
                GridRow {
                    Text("Agent").foregroundStyle(.secondary)
                    Text(session.agentId)
                }
                GridRow {
                    Text("Namespace").foregroundStyle(.secondary)
                    Text(session.namespace)
                }
                if !session.description.isEmpty {
                    GridRow {
                        Text("Description").foregroundStyle(.secondary)
                        Text(session.description)
                    }
                }
                GridRow {
                    Text("Started").foregroundStyle(.secondary)
                    Text(session.startedAt)
                }
                if let endedAt = session.endedAt {
                    GridRow {
                        Text("Ended").foregroundStyle(.secondary)
                        Text(endedAt)
                    }
                }
                GridRow {
                    Text("Entries").foregroundStyle(.secondary)
                    Text("\(session.entryCount)")
                }
                GridRow {
                    Text("Tokens").foregroundStyle(.secondary)
                    Text("\(session.totalTokens)")
                }
            }
            .font(.subheadline)
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
