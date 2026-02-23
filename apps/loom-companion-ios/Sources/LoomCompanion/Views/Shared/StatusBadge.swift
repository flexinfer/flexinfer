import SwiftUI
import LoomCompanionKit

struct StatusBadge: View {
    let status: String
    let color: Color

    init(sessionStatus: SessionStatus) {
        switch sessionStatus {
        case .active:
            status = "Active"
            color = .green
        case .ended:
            status = "Ended"
            color = .secondary
        }
    }

    init(healthStatus: OverallHealthStatus) {
        switch healthStatus {
        case .healthy:
            status = "Healthy"
            color = .green
        case .degraded:
            status = "Degraded"
            color = .orange
        case .critical:
            status = "Critical"
            color = .red
        case .unknown:
            status = "Unknown"
            color = .secondary
        }
    }

    init(_ text: String, color: Color) {
        self.status = text
        self.color = color
    }

    var body: some View {
        Text(status)
            .font(.caption2)
            .fontWeight(.medium)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(color.opacity(0.15))
            .foregroundStyle(color)
            .clipShape(Capsule())
    }
}
