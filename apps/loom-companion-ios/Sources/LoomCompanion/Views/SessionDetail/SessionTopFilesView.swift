import SwiftUI
import LoomCompanionKit

struct SessionTopFilesView: View {
    let files: [TouchedFile]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("Top Files", systemImage: "doc.text")
                    .font(.headline)
                Spacer()
                Text("\(files.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            ForEach(files) { file in
                HStack {
                    Text(file.filePath.components(separatedBy: "/").last ?? file.filePath)
                        .font(.subheadline)
                        .monospaced()
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                    Text("\(file.touchCount)×")
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
