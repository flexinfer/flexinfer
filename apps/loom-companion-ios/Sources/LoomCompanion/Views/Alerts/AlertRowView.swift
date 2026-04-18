import SwiftUI
import LoomCompanionKit

struct AlertRowView: View {
    let alert: AlertItem

    private var accent: Color {
        switch alert.severity {
        case .critical: return LoomColors.statusCritical
        case .warning: return LoomColors.statusDegraded
        case .info: return LoomColors.statusInfo
        }
    }

    private var severityIconName: String {
        switch alert.severity {
        case .critical: return "exclamationmark.triangle.fill"
        case .warning: return "exclamationmark.circle.fill"
        case .info: return "info.circle.fill"
        }
    }

    /// Critical unread alerts pulse their accent + take emphasized typography.
    /// Read alerts are muted (opacity) but retain the accent bar so they're still scannable.
    private var isUrgent: Bool {
        !alert.isRead && alert.severity == .critical
    }

    @ViewBuilder
    private var actionPill: some View {
        switch alert.primaryAction {
        case .viewSession:
            LoomPill("Session", icon: "arrow.right.circle", color: LoomColors.accent, style: .outlined, weight: .micro)
        case .viewWorkflow:
            LoomPill("Workflow", icon: "arrow.right.circle", color: LoomColors.accent, style: .outlined, weight: .micro)
        case .viewDashboard:
            LoomPill("Dashboard", icon: "arrow.right.circle", color: LoomColors.accent, style: .outlined, weight: .micro)
        case .acknowledge:
            EmptyView()
        }
    }

    /// Compact relative-time label shown as a muted micro pill.
    private var relativeTime: String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: alert.timestamp, relativeTo: Date())
    }

    var body: some View {
        LoomListRow(
            accentColor: accent,
            title: alert.title,
            subtitle: alert.message,
            isLive: isUrgent,
            emphasizeTitle: !alert.isRead,
            leading: {
                LoomRowIcon(systemName: severityIconName, color: accent, size: 12)
                    .symbolEffect(.variableColor.iterative, isActive: isUrgent)
            },
            trailing: {
                HStack(spacing: LoomSpacing.xxs) {
                    if !alert.isRead {
                        Circle()
                            .fill(LoomColors.statusActive)
                            .frame(width: 7, height: 7)
                            .accessibilityLabel("Unread")
                    }
                    Text(relativeTime)
                        .font(LoomTypography.monoCaption)
                        .foregroundStyle(LoomColors.textTertiary)
                        .monospacedDigit()
                }
            },
            footer: {
                LoomPill(
                    alert.severity.rawValue.uppercased(),
                    color: accent,
                    style: isUrgent ? .solid : .tinted,
                    weight: .micro
                )
                actionPill
            }
        )
        .opacity(alert.isRead ? 0.72 : 1.0)
        .listRowBackground(
            alert.isRead
                ? Color.clear
                : LoomColors.severityBackground(alert.severity)
        )
    }
}

#Preview("AlertRowView · states") {
    VStack(spacing: 0) {
        AlertRowView(alert: AlertItem(
            severity: .critical,
            interruptionLevel: .timeSensitive,
            title: "Server Down",
            message: "2 server(s) down — control plane reconnecting",
            eventType: "hud.health",
            allowedActions: [.viewDashboard, .acknowledge],
            isRead: false
        ))
        Divider().overlay(LoomColors.border)
        AlertRowView(alert: AlertItem(
            timestamp: Date().addingTimeInterval(-300),
            severity: .warning,
            title: "Session Reaped",
            message: "Session svc-abc123 reaped after 30m idle",
            eventType: "agent.session.reaped",
            allowedActions: [.viewSession, .acknowledge],
            isRead: false
        ))
        Divider().overlay(LoomColors.border)
        AlertRowView(alert: AlertItem(
            timestamp: Date().addingTimeInterval(-7200),
            severity: .info,
            title: "Session Started",
            message: "Agent claude-code started a new session",
            eventType: "agent.session.start",
            allowedActions: [.viewSession, .acknowledge],
            isRead: true
        ))
    }
    .padding(.horizontal, 12)
    .background(LoomColors.bgPrimary)
    .preferredColorScheme(.dark)
}
