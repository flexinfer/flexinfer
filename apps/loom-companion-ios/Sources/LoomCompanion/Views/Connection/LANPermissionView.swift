import SwiftUI

struct LANPermissionView: View {
    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "wifi.exclamationmark")
                .font(.system(size: 40))
                .foregroundStyle(LoomColors.statusDegraded)

            Text("Local Network Access Required")
                .font(.headline)

            Text("Loom Companion needs local network access to communicate with your Loom HUD on the same network.")
                .font(.subheadline)
                .foregroundStyle(LoomColors.fgSecondary)
                .multilineTextAlignment(.center)

            Text("Go to Settings > Privacy & Security > Local Network and enable access for Loom Companion.")
                .font(.caption)
                .foregroundStyle(LoomColors.fgSecondary)
                .multilineTextAlignment(.center)

            #if os(iOS)
            Button {
                if let url = URL(string: UIApplication.openSettingsURLString) {
                    UIApplication.shared.open(url)
                }
            } label: {
                Label("Open Settings", systemImage: "gear")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            #endif
        }
        .padding()
        .background(LoomColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}
