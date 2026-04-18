import SwiftUI
import LoomCompanionKit

struct StatusAccentBar: View {
    let color: Color
    let isLive: Bool
    let prominent: Bool

    @State private var pulseOn = false

    init(color: Color, isLive: Bool = false, prominent: Bool = false) {
        self.color = color
        self.isLive = isLive
        self.prominent = prominent
    }

    init(sessionStatus: SessionStatus, prominent: Bool = false) {
        self.color = LoomColors.sessionStatusColor(sessionStatus)
        self.isLive = sessionStatus == .active
        self.prominent = prominent
    }

    private var width: CGFloat {
        prominent ? LoomSpacing.accentBarWidthProminent : LoomSpacing.accentBarWidth
    }

    var body: some View {
        RoundedRectangle(cornerRadius: LoomSpacing.accentBarCornerRadius)
            .fill(
                LinearGradient(
                    colors: [
                        color.opacity(isLive ? (pulseOn ? 1.0 : 0.75) : 0.9),
                        color.opacity(isLive ? (pulseOn ? 0.55 : 0.35) : 0.5)
                    ],
                    startPoint: .top,
                    endPoint: .bottom
                )
            )
            .frame(width: width)
            .loomStatusGlow(isLive ? color.opacity(pulseOn ? 1.0 : 0.6) : color)
            .onAppear {
                guard isLive else { return }
                withAnimation(.easeInOut(duration: 1.4).repeatForever(autoreverses: true)) {
                    pulseOn = true
                }
            }
    }
}

#Preview("StatusAccentBar") {
    VStack(spacing: 14) {
        HStack(spacing: 10) {
            StatusAccentBar(color: LoomColors.statusHealthy, isLive: true, prominent: true)
                .frame(height: 56)
            Text("Live · pulsing · prominent")
                .foregroundStyle(LoomColors.fgPrimary)
        }
        HStack(spacing: 10) {
            StatusAccentBar(color: LoomColors.statusDegraded, prominent: true)
                .frame(height: 56)
            Text("Degraded · prominent")
                .foregroundStyle(LoomColors.fgPrimary)
        }
        HStack(spacing: 10) {
            StatusAccentBar(color: LoomColors.statusIdle)
                .frame(height: 56)
            Text("Idle · thin")
                .foregroundStyle(LoomColors.fgSecondary)
        }
        HStack(spacing: 10) {
            StatusAccentBar(color: LoomColors.statusCritical, isLive: true, prominent: true)
                .frame(height: 56)
            Text("Critical · pulsing")
                .foregroundStyle(LoomColors.fgPrimary)
        }
    }
    .padding()
    .background(LoomColors.bgPrimary)
}
