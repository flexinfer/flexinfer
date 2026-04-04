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
                    Text("Name").foregroundStyle(LoomColors.fgSecondary)
                    Text(profile.name)
                }
                GridRow {
                    Text("URL").foregroundStyle(LoomColors.fgSecondary)
                    Text(profile.baseURL)
                        .monospaced()
                        .font(.caption)
                }
                GridRow {
                    Text("Mode").foregroundStyle(LoomColors.fgSecondary)
                    HStack(spacing: 4) {
                        Image(systemName: profile.mode == .lan ? "wifi" : "globe")
                        Text(profile.mode.rawValue.uppercased())
                    }
                }
                if profile.mode == .gateway {
                    GridRow {
                        Text("CF Access").foregroundStyle(LoomColors.fgSecondary)
                        Text(profile.hasCloudflareAccessServiceToken ? "Configured" : "Not configured")
                    }
                }
            }
            .font(.subheadline)
        }
        .padding()
        .background(LoomColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
