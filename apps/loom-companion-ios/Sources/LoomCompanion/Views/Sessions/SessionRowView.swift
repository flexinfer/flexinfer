import SwiftUI
import LoomCompanionKit

struct SessionRowView: View {
    let session: SessionInfo

    var body: some View {
        HStack(spacing: LoomSpacing.sm) {
            StatusAccentBar(sessionStatus: session.status)
                .frame(height: nil)

            VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                HStack {
                    Image(systemName: LoomColors.agentTypeIcon(session.agentId))
                        .font(.system(size: 10))
                        .foregroundStyle(LoomColors.agentTypeColor(session.agentId))

                    Text(session.agentId)
                        .font(LoomTypography.bodyMedium)
                    Spacer()
                    StatusBadge(sessionStatus: session.status)
                }

                Text(session.namespace)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textSecondary)

                if !session.description.isEmpty {
                    Text(session.description)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                        .lineLimit(2)
                }

                HStack(spacing: LoomSpacing.md) {
                    Label("\(session.entryCount)", systemImage: "doc.text")
                    Label("\(session.totalTokens)", systemImage: "number")

                    if session.status == .active {
                        Spacer()
                        Label("Live", systemImage: "circle.fill")
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.statusHealthy)
                            .symbolEffect(.pulse, isActive: true)
                    } else {
                        Spacer()
                        Text(session.startedAt)
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                }
                .font(LoomTypography.labelSmall)
                .foregroundStyle(LoomColors.textSecondary)
            }
        }
        .padding(.vertical, LoomSpacing.xxs)
    }
}
