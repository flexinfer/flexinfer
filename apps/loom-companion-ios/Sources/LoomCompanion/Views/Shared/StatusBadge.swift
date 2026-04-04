import SwiftUI
import LoomCompanionKit

struct StatusBadge: View {
    let status: String
    let color: Color
    let icon: String?

    init(sessionStatus: SessionStatus) {
        switch sessionStatus {
        case .active:
            status = "Active"; color = LoomColors.statusHealthy; icon = "circle.fill"
        case .ended:
            status = "Ended"; color = LoomColors.fgMuted; icon = "checkmark.circle"
        case .summarized:
            status = "Summarized"; color = LoomColors.info; icon = "doc.text.fill"
        case .unknown:
            status = "Unknown"; color = LoomColors.fgMuted; icon = nil
        }
    }

    init(healthStatus: OverallHealthStatus) {
        switch healthStatus {
        case .healthy:
            status = "Healthy"; color = LoomColors.statusHealthy; icon = "heart.fill"
        case .degraded:
            status = "Degraded"; color = LoomColors.statusDegraded; icon = "exclamationmark.triangle"
        case .critical:
            status = "Critical"; color = LoomColors.statusCritical; icon = "xmark.octagon.fill"
        case .unknown:
            status = "Unknown"; color = LoomColors.fgMuted; icon = nil
        }
    }

    init(presenceStatus: MobilePresenceStatus) {
        switch presenceStatus {
        case .active:
            status = "Active"; color = LoomColors.statusHealthy; icon = "circle.fill"
        case .idle:
            status = "Idle"; color = LoomColors.statusDegraded; icon = "moon.fill"
        case .offline:
            status = "Offline"; color = LoomColors.statusIdle; icon = "circle.dashed"
        case .unknown:
            status = "Unknown"; color = LoomColors.fgMuted; icon = nil
        }
    }

    init(_ text: String, color: Color, icon: String? = nil) {
        self.status = text
        self.color = color
        self.icon = icon
    }

    var body: some View {
        HStack(spacing: 4) {
            if let icon {
                Image(systemName: icon)
                    .font(.system(size: 8))
                    .symbolEffect(
                        .pulse,
                        isActive: status == "Active" || status == "Critical"
                    )
            }
            Text(status)
                .font(.caption2)
                .fontWeight(.medium)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(color.opacity(0.12))
        .foregroundStyle(color)
        .clipShape(Capsule())
    }
}
