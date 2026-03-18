import SwiftUI
import LoomCompanionKit

struct SessionEntriesSection: View {
    let title: String
    let icon: String
    let entries: [SessionTopEntry]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label(title, systemImage: icon)
                    .font(.headline)
                Spacer()
                Text("\(entries.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            ForEach(entries) { entry in
                VStack(alignment: .leading, spacing: 4) {
                    Text(entry.title)
                        .font(.subheadline)
                    HStack {
                        Text(entry.entryType)
                            .font(.caption2)
                            .monospaced()
                            .foregroundStyle(.secondary)
                        Spacer()
                        Text(entry.timestamp)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
                if entry.id != entries.last?.id {
                    Divider()
                }
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
