import SwiftUI
import LoomCompanionKit

struct ConnectionProfileView: View {
    let profile: ConnectionProfile

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Connection Profile")
                .font(.headline)

            Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("Name").foregroundStyle(.secondary)
                    Text(profile.name)
                }
                GridRow {
                    Text("URL").foregroundStyle(.secondary)
                    Text(profile.baseURL)
                        .monospaced()
                        .font(.caption)
                }
                GridRow {
                    Text("Mode").foregroundStyle(.secondary)
                    HStack(spacing: 4) {
                        Image(systemName: profile.mode == .lan ? "wifi" : "globe")
                        Text(profile.mode.rawValue.uppercased())
                    }
                }
            }
            .font(.subheadline)
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
