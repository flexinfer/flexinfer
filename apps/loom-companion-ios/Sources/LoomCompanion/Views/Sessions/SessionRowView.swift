import SwiftUI
import LoomCompanionKit

struct SessionRowView: View {
    let session: SessionInfo

    private var isLive: Bool { session.status == .active }

    private var subtitle: String {
        // Compose one scannable context line: namespace · description (if present)
        if session.description.isEmpty {
            return session.namespace
        }
        return "\(session.namespace) · \(session.description)"
    }

    /// Choose the single most actionable metric to surface on the right.
    /// Live sessions highlight token count (active work indicator).
    /// Terminal sessions show start time for recency scanning.
    @ViewBuilder
    private var primaryMetric: some View {
        if isLive {
            LoomRowMetric(
                formatTokens(session.totalTokens),
                unit: nil,
                color: LoomColors.statusHealthy
            )
        } else {
            LoomRowMetric(
                formatRelative(session.startedAt),
                color: LoomColors.textTertiary
            )
        }
    }

    @ViewBuilder
    private var footerPills: some View {
        if isLive {
            LoomPill(
                "live",
                icon: "bolt.fill",
                color: LoomColors.statusActive,
                weight: .micro
            )
        }
        LoomPill(
            "\(session.entryCount) entries",
            icon: "doc.text",
            color: LoomColors.accent,
            style: .outlined,
            weight: .micro
        )
        if !isLive {
            LoomPill(
                formatTokens(session.totalTokens),
                color: LoomColors.textSecondary,
                style: .outlined,
                weight: .micro
            )
        }
    }

    var body: some View {
        LoomListRow(
            accentColor: LoomColors.sessionStatusColor(session.status),
            title: session.agentId,
            subtitle: subtitle,
            isLive: isLive,
            emphasizeTitle: isLive,
            leading: {
                LoomRowIcon(
                    systemName: LoomColors.agentTypeIcon(session.agentId),
                    color: LoomColors.agentTypeColor(session.agentId)
                )
            },
            trailing: { primaryMetric },
            footer: { footerPills }
        )
        .loomShareContextMenu(.session(id: session.id))
    }

    // MARK: - Formatting Helpers

    private func formatTokens(_ tokens: Int) -> String {
        if tokens >= 1_000_000 {
            return String(format: "%.1fM", Double(tokens) / 1_000_000.0)
        }
        if tokens >= 1_000 {
            return String(format: "%.1fk", Double(tokens) / 1_000.0)
        }
        return "\(tokens)"
    }

    private static let isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let isoFallback: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private func formatRelative(_ iso: String) -> String {
        guard let date = Self.isoFormatter.date(from: iso) ?? Self.isoFallback.date(from: iso) else {
            return iso
        }
        let diff = Int(Date().timeIntervalSince(date))
        if diff < 60 { return "\(max(0, diff))s" }
        if diff < 3600 { return "\(diff / 60)m" }
        if diff < 86_400 { return "\(diff / 3600)h" }
        return "\(diff / 86_400)d"
    }
}

#Preview("SessionRowView · states") {
    VStack(spacing: 0) {
        SessionRowView(session: SessionInfo(
            id: "s1",
            agentId: "claude-code",
            namespace: "services/loom-core",
            status: .active,
            description: "Frontend UX craft slice 1",
            startedAt: ISO8601DateFormatter().string(from: Date().addingTimeInterval(-420)),
            entryCount: 42,
            totalTokens: 12_400
        ))
        Divider().overlay(LoomColors.border)
        SessionRowView(session: SessionInfo(
            id: "s2",
            agentId: "codex",
            namespace: "platform/gitops",
            status: .summarized,
            description: "Reconcile Flux drift",
            startedAt: ISO8601DateFormatter().string(from: Date().addingTimeInterval(-7200)),
            entryCount: 18,
            totalTokens: 3_200
        ))
        Divider().overlay(LoomColors.border)
        SessionRowView(session: SessionInfo(
            id: "s3",
            agentId: "gemini",
            namespace: "libs/svg-sdk",
            status: .ended,
            description: "",
            startedAt: ISO8601DateFormatter().string(from: Date().addingTimeInterval(-86_400)),
            entryCount: 5,
            totalTokens: 820
        ))
    }
    .padding(.horizontal, 12)
    .background(LoomColors.bgPrimary)
    .preferredColorScheme(.dark)
}
