import SwiftUI
import LoomCompanionKit

struct SessionEntryBreakdownView: View {
    let buckets: [EntryTypeBucket]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("Context Entries", systemImage: "square.stack.3d.up")
                    .font(.headline)
                Spacer()
                Text("\(buckets.reduce(0) { $0 + $1.count })")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            ForEach(buckets) { bucket in
                HStack {
                    Text(bucket.entryType)
                        .font(.subheadline)
                        .monospaced()
                    Spacer()
                    Text("\(bucket.count)")
                        .font(.subheadline)
                        .fontWeight(.medium)
                    Text("·")
                        .foregroundStyle(.tertiary)
                    Text("~\(bucket.estimatedTokens) tok")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
