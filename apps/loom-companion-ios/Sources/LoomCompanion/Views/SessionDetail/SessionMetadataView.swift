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
                    Text("ID").foregroundStyle(LoomColors.fgSecondary)
                    Text(session.id).font(.caption).monospaced()
                }
                GridRow {
                    Text("Agent").foregroundStyle(LoomColors.fgSecondary)
                    Text(session.agentId)
                }
                GridRow {
                    Text("Namespace").foregroundStyle(LoomColors.fgSecondary)
                    Text(session.namespace)
                }
                if !session.description.isEmpty {
                    GridRow {
                        Text("Description").foregroundStyle(LoomColors.fgSecondary)
                        Text(session.description)
                    }
                }
                GridRow {
                    Text("Started").foregroundStyle(LoomColors.fgSecondary)
                    Text(session.startedAt)
                }
                if let endedAt = session.endedAt {
                    GridRow {
                        Text("Ended").foregroundStyle(LoomColors.fgSecondary)
                        Text(endedAt)
                    }
                }
                GridRow {
                    Text("Entries").foregroundStyle(LoomColors.fgSecondary)
                    Text("\(session.entryCount)")
                }
                GridRow {
                    Text("Tokens").foregroundStyle(LoomColors.fgSecondary)
                    Text("\(session.totalTokens)")
                }
            }
            .font(.subheadline)
        }
        .padding()
        .background(LoomColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
