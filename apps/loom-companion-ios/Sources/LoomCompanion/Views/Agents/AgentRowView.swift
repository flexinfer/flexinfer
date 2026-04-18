import SwiftUI
import LoomCompanionKit

struct AgentRowView: View {
    let agent: UnifiedAgent

    private var isLive: Bool { agent.status == .active }

    /// Secondary lane: project/namespace · current task or description.
    private var subtitle: String {
        var parts: [String] = []
        if let project = agent.project, !project.isEmpty {
            parts.append(project)
        } else if let ns = agent.namespace, !ns.isEmpty {
            parts.append(ns)
        }
        if !agent.currentTask.isEmpty {
            parts.append(agent.currentTask)
        } else if !agent.description.isEmpty {
            parts.append(agent.description)
        }
        return parts.joined(separator: " · ")
    }

    /// One-glance metric: tokens when working, last-seen when not.
    @ViewBuilder
    private var primaryMetric: some View {
        if isLive, agent.totalTokens > 0 {
            LoomRowMetric(
                formatTokens(agent.totalTokens),
                unit: "tok",
                color: LoomColors.statusHealthy
            )
        } else if agent.heartbeatAgeSeconds > 0 {
            LoomRowMetric(
                Self.compactDuration(agent.heartbeatAgeSeconds),
                unit: isLive ? nil : "ago",
                color: LoomColors.textTertiary
            )
        } else {
            EmptyView()
        }
    }

    var body: some View {
        LoomListRow(
            accentColor: LoomColors.presenceStatusColor(agent.status),
            title: agent.agentId,
            subtitle: subtitle,
            isLive: isLive,
            needsAttention: agent.needsAttention,
            emphasizeTitle: isLive,
            leading: {
                LoomRowIcon(
                    systemName: LoomColors.agentTypeIcon(agent.agentType),
                    color: LoomColors.agentTypeColor(agent.agentType)
                )
            },
            trailing: { primaryMetric },
            footer: { pillStrip }
        )
        .loomShareContextMenu(.agent(id: agent.agentId))
    }

    // MARK: - Pill Strip (footer lane)

    /// Flexible metadata strip. Order signals importance: live/status first,
    /// then branch, blockers, session evidence, spawn, pipeline.
    @ViewBuilder
    private var pillStrip: some View {
        // Telemetry / live badge
        if isLive {
            LoomPill(
                "live",
                icon: "bolt.fill",
                color: LoomColors.statusActive,
                weight: .micro
            )
        } else if !agent.telemetryStatus.isEmpty && agent.telemetryStatus != "offline" {
            LoomPill(
                agent.telemetryStatus,
                color: telemetryColor,
                style: .outlined,
                weight: .micro
            )
        }

        // Branch — high signal for working agents
        if !agent.branch.isEmpty {
            LoomPill(
                agent.branch,
                icon: "point.3.connected.trianglepath.dotted",
                color: LoomColors.accent,
                weight: .micro
            )
        }

        // Blocked tasks — demands attention, surface early
        if agent.blockedTasks > 0 {
            LoomPill(
                "\(agent.blockedTasks) blocked",
                icon: "exclamationmark.triangle.fill",
                color: LoomColors.statusBlocked,
                weight: .micro
            )
        } else if agent.taskCount > 0 {
            LoomPill(
                "\(agent.taskCount) tasks",
                icon: "checklist",
                color: LoomColors.textSecondary,
                style: .outlined,
                weight: .micro
            )
        }

        // Session evidence — entry count
        if agent.hasSession, agent.entryCount > 0 {
            LoomPill(
                "\(agent.entryCount)",
                icon: "doc.text",
                color: LoomColors.accent,
                style: .outlined,
                weight: .micro
            )
        }

        // Spawned (remote execution)
        if agent.isSpawned {
            LoomPill(
                "k8s",
                icon: "cloud",
                color: LoomColors.tierShortTerm,
                weight: .micro
            )
        }

        // CI state — only if meaningful
        if let status = agent.pipelineStatus, !status.isEmpty {
            LoomPill(
                "CI \(status)",
                icon: "arrow.triangle.2.circlepath",
                color: pipelineColor(status),
                weight: .micro
            )
        }
    }

    // MARK: - Color helpers

    private var telemetryColor: Color {
        switch agent.telemetryStatus {
        case "live": return LoomColors.statusActive
        case "idle": return LoomColors.statusIdle
        case "stale": return LoomColors.statusDegraded
        case "session_only": return LoomColors.accent
        case "offline": return LoomColors.textTertiary
        default: return LoomColors.textSecondary
        }
    }

    private func pipelineColor(_ status: String) -> Color {
        switch status {
        case "running": return LoomColors.statusActive
        case "success": return LoomColors.statusHealthy
        case "failed": return LoomColors.statusCritical
        case "pending": return LoomColors.statusIdle
        default: return LoomColors.textTertiary
        }
    }

    // MARK: - Formatting

    private func formatTokens(_ tokens: Int) -> String {
        if tokens >= 1_000_000 {
            return String(format: "%.1fM", Double(tokens) / 1_000_000.0)
        }
        if tokens >= 1_000 {
            return String(format: "%.1fk", Double(tokens) / 1_000.0)
        }
        return "\(tokens)"
    }

    static func compactDuration(_ seconds: Int) -> String {
        if seconds < 60 { return "\(seconds)s" }
        if seconds < 3600 { return "\(seconds / 60)m" }
        if seconds < 86_400 { return "\(seconds / 3600)h" }
        return "\(seconds / 86_400)d"
    }

    // Kept for backward compatibility with any existing callers.
    static func relativeTime(_ iso: String, now: Date) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let fallback = ISO8601DateFormatter()
        fallback.formatOptions = [.withInternetDateTime]
        guard let date = formatter.date(from: iso) ?? fallback.date(from: iso) else { return "" }
        let diff = Int(now.timeIntervalSince(date))
        if diff < 0 { return "just now" }
        if diff < 5 { return "just now" }
        if diff < 60 { return "\(diff)s ago" }
        if diff < 3600 { return "\(diff / 60)m\(diff % 60)s ago" }
        return "\(diff / 3600)h ago"
    }
}
