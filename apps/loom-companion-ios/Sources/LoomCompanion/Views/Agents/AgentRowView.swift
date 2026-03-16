import SwiftUI
import LoomCompanionKit

struct AgentRowView: View {
    let agent: UnifiedAgent

    var body: some View {
        HStack(spacing: LoomSpacing.sm) {
            StatusAccentBar(color: LoomColors.presenceStatusColor(agent.status))

            VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                HStack {
                    Image(systemName: agentTypeIcon)
                        .font(.system(size: 14))
                        .foregroundStyle(LoomColors.presenceStatusColor(agent.status))

                    Text(agent.agentId)
                        .font(LoomTypography.headlineSmall)
                        .lineLimit(1)

                    Spacer()

                    StatusBadge(presenceStatus: agent.status)
                }

                if !agent.currentTask.isEmpty {
                    Text(agent.currentTask)
                        .font(LoomTypography.bodySmall)
                        .foregroundStyle(LoomColors.textSecondary)
                        .lineLimit(2)
                } else if !agent.description.isEmpty {
                    Text(agent.description)
                        .font(LoomTypography.bodySmall)
                        .foregroundStyle(LoomColors.textSecondary)
                        .lineLimit(2)
                }

                HStack(spacing: LoomSpacing.sm) {
                    if !agent.branch.isEmpty {
                        Label(agent.branch, systemImage: "arrow.triangle.branch")
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                            .lineLimit(1)
                    }

                    if let ns = agent.namespace, !ns.isEmpty {
                        Label(ns, systemImage: "folder")
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                            .lineLimit(1)
                    }
                }

                HStack(spacing: LoomSpacing.sm) {
                    if agent.hasSession {
                        sessionPill
                    }
                    if agent.isSpawned {
                        spawnPill
                    }
                    Spacer()
                    elapsedLabel
                }
            }
        }
        .padding(.vertical, LoomSpacing.xxs)
    }

    private var agentTypeIcon: String {
        switch agent.agentType.lowercased() {
        case "claude-code": return "brain.head.profile"
        case "codex": return "terminal"
        case "gemini": return "sparkles"
        default: return "desktopcomputer"
        }
    }

    private var sessionPill: some View {
        HStack(spacing: 3) {
            Image(systemName: "doc.text")
                .font(.system(size: 8))
            Text("\(agent.entryCount) entries")
            if agent.totalTokens > 0 {
                Text("·")
                Text(formatTokens(agent.totalTokens))
            }
        }
        .font(.caption2)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(LoomColors.accent.opacity(0.1))
        .foregroundStyle(LoomColors.accent)
        .clipShape(Capsule())
    }

    private var spawnPill: some View {
        HStack(spacing: 3) {
            Image(systemName: "cloud")
                .font(.system(size: 8))
            Text("K8s")
            if let status = agent.spawnStatus {
                Text(status)
            }
        }
        .font(.caption2)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(Color.purple.opacity(0.1))
        .foregroundStyle(.purple)
        .clipShape(Capsule())
    }

    private var elapsedLabel: some View {
        Group {
            if agent.status == .active {
                HStack(spacing: 2) {
                    PulsingDot(color: LoomColors.presenceStatusColor(agent.status), size: 6)
                    if !agent.lastHeartbeat.isEmpty {
                        TimelineView(.periodic(from: .now, by: 1)) { context in
                            Text(Self.relativeTime(agent.lastHeartbeat, now: context.date))
                                .font(LoomTypography.monoCaption)
                                .foregroundStyle(LoomColors.textTertiary)
                        }
                    }
                }
            } else if !agent.lastHeartbeat.isEmpty {
                Text("Last seen \(Self.relativeTime(agent.lastHeartbeat, now: Date()))")
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.textTertiary)
            }
        }
    }

    private func formatTokens(_ tokens: Int) -> String {
        if tokens >= 1000 {
            return String(format: "%.1fk tok", Double(tokens) / 1000.0)
        }
        return "\(tokens) tok"
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

    static func relativeTime(_ iso: String, now: Date) -> String {
        guard let date = isoFormatter.date(from: iso) ?? isoFallback.date(from: iso) else { return "" }
        let diff = Int(now.timeIntervalSince(date))
        if diff < 0 { return "just now" }
        if diff < 5 { return "just now" }
        if diff < 60 { return "\(diff)s ago" }
        if diff < 3600 { return "\(diff / 60)m\(diff % 60)s ago" }
        return "\(diff / 3600)h ago"
    }
}
