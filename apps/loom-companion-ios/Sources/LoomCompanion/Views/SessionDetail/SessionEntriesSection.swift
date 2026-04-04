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
                    .foregroundStyle(LoomColors.fgSecondary)
            }

            ForEach(entries) { entry in
                VStack(alignment: .leading, spacing: 4) {
                    Text(entry.title)
                        .font(.subheadline)
                    HStack {
                        Text(entry.entryType)
                            .font(.caption2)
                            .monospaced()
                            .foregroundStyle(LoomColors.fgSecondary)
                        Spacer()
                        Text(entry.timestamp)
                            .font(.caption2)
                            .foregroundStyle(LoomColors.fgMuted)
                    }
                }
                if entry.id != entries.last?.id {
                    Divider()
                }
            }
        }
        .padding()
        .background(LoomColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
