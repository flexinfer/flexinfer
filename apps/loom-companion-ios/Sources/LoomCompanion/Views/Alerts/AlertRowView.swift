import SwiftUI
import LoomCompanionKit

struct AlertRowView: View {
    let alert: AlertItem

    var body: some View {
        HStack(spacing: 12) {
            severityIcon
                .frame(width: 28, height: 28)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(alert.title)
                        .font(.headline)
                        .fontWeight(alert.isRead ? .regular : .bold)

                    Spacer()

                    if !alert.isRead {
                        Circle()
                            .fill(.blue)
                            .frame(width: 8, height: 8)
                    }
                }

                Text(alert.message)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)

                HStack {
                    Text(alert.timestamp, style: .relative)
                        .font(.caption)
                        .foregroundStyle(.tertiary)

                    if alert.primaryAction != .acknowledge {
                        Spacer()
                        actionLabel
                    }
                }
            }
        }
        .padding(.vertical, 4)
        .opacity(alert.isRead ? 0.7 : 1.0)
    }

    @ViewBuilder
    private var severityIcon: some View {
        switch alert.severity {
        case .critical:
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
                .font(.title3)
        case .warning:
            Image(systemName: "exclamationmark.circle.fill")
                .foregroundStyle(.orange)
                .font(.title3)
        case .info:
            Image(systemName: "info.circle.fill")
                .foregroundStyle(.blue)
                .font(.title3)
        }
    }

    @ViewBuilder
    private var actionLabel: some View {
        switch alert.primaryAction {
        case .viewSession:
            Label("Session", systemImage: "arrow.right.circle")
                .font(.caption)
                .foregroundStyle(.secondary)
        case .viewDashboard:
            Label("Dashboard", systemImage: "arrow.right.circle")
                .font(.caption)
                .foregroundStyle(.secondary)
        case .acknowledge:
            EmptyView()
        }
    }
}
