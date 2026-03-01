import SwiftUI
import LoomCompanionKit

struct AlertRowView: View {
    let alert: AlertItem

    var body: some View {
        HStack(spacing: LoomSpacing.md) {
            severityIcon
                .frame(width: 28, height: 28)

            VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                HStack {
                    Text(alert.title)
                        .font(LoomTypography.headlineMedium)
                        .fontWeight(alert.isRead ? .regular : .bold)

                    Spacer()

                    if !alert.isRead {
                        PulsingDot(color: LoomColors.statusActive, size: 8, isPulsing: true)
                    }
                }

                Text(alert.message)
                    .font(LoomTypography.bodyRegular)
                    .foregroundStyle(LoomColors.textSecondary)
                    .lineLimit(2)

                HStack {
                    Text(alert.timestamp, style: .relative)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)

                    if alert.primaryAction != .acknowledge {
                        Spacer()
                        actionLabel
                    }
                }
            }
        }
        .padding(.vertical, LoomSpacing.xxs)
        .opacity(alert.isRead ? 0.7 : 1.0)
        .listRowBackground(
            alert.isRead
                ? Color.clear
                : LoomColors.severityBackground(alert.severity)
        )
    }

    @ViewBuilder
    private var severityIcon: some View {
        switch alert.severity {
        case .critical:
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(LoomColors.statusCritical)
                .font(.title3)
                .symbolEffect(.variableColor.iterative, isActive: !alert.isRead)
        case .warning:
            Image(systemName: "exclamationmark.circle.fill")
                .foregroundStyle(LoomColors.statusDegraded)
                .font(.title3)
        case .info:
            Image(systemName: "info.circle.fill")
                .foregroundStyle(LoomColors.statusInfo)
                .font(.title3)
        }
    }

    @ViewBuilder
    private var actionLabel: some View {
        switch alert.primaryAction {
        case .viewSession:
            Label("Session", systemImage: "arrow.right.circle")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        case .viewWorkflow:
            Label("Workflow", systemImage: "arrow.right.circle")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        case .viewDashboard:
            Label("Dashboard", systemImage: "arrow.right.circle")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        case .acknowledge:
            EmptyView()
        }
    }
}
