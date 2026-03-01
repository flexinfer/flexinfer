import SwiftUI
import LoomCompanionKit

struct StatusAccentBar: View {
    let color: Color

    init(color: Color) {
        self.color = color
    }

    init(sessionStatus: SessionStatus) {
        self.color = LoomColors.sessionStatusColor(sessionStatus)
    }

    var body: some View {
        RoundedRectangle(cornerRadius: LoomSpacing.accentBarCornerRadius)
            .fill(color)
            .frame(width: LoomSpacing.accentBarWidth)
            .loomStatusGlow(color)
    }
}

#Preview("StatusAccentBar") {
    VStack(spacing: 12) {
        HStack(spacing: 8) {
            StatusAccentBar(color: .green)
                .frame(height: 48)
            Text("Active session")
        }
        HStack(spacing: 8) {
            StatusAccentBar(color: .orange)
                .frame(height: 48)
            Text("Degraded service")
        }
        HStack(spacing: 8) {
            StatusAccentBar(color: .red)
                .frame(height: 48)
            Text("Critical alert")
        }
    }
    .padding()
}
