import SwiftUI
import LoomCompanionKit

struct AgentRowView: View {
    let agent: UnifiedAgent

    var body: some View {
        HStack(spacing: LoomSpacing.sm) {
            StatusAccentBar(color: LoomColors.presenceStatusColor(agent.status))

            VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                // Line 1: status dot + type icon + agent_id + branch pill + token count
                HStack(spacing: LoomSpacing.xs) {
                    Circle()
                        .fill(LoomColors.presenceStatusColor(agent.status))
                        .frame(width: 8, height: 8)

                    Image(systemName: LoomColors.agentTypeIcon(agent.agentType))
                        .font(.system(size: 10))
                        .foregroundStyle(LoomColors.agentTypeColor(agent.agentType))

                    Text(agent.agentId)
                        .font(LoomTypography.bodyMedium)
                        .lineLimit(1)

                    Spacer()

                    if !agent.branch.isEmpty {
                        branchPill
                    }

                    if agent.totalTokens > 0 {
                        Text(formatTokens(agent.totalTokens))
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                    }
                }

                // Line 2: project/namespace + current task / description
                HStack(spacing: LoomSpacing.xs) {
                    if let project = agent.project, !project.isEmpty {
                        Text(project)
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                            .lineLimit(1)
                    } else if let ns = agent.namespace, !ns.isEmpty {
                        Text(ns)
                            .font(LoomTypography.monoCaption)
                            .foregroundStyle(LoomColors.textTertiary)
                            .lineLimit(1)
                    }

                    if !agent.currentTask.isEmpty {
                        Text(agent.currentTask)
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                            .lineLimit(1)
                    } else if !agent.description.isEmpty {
                        Text(agent.description)
                            .font(LoomTypography.caption)
                            .foregroundStyle(LoomColors.textSecondary)
                            .lineLimit(1)
                    }

                    Spacer()

                    // Inline indicators
                    if agent.needsAttention {
                        Image(systemName: "exclamationmark.circle.fill")
                            .font(.system(size: 10))
                            .foregroundStyle(.orange)
                    }
                }

                // Line 3: compact pill row
                HStack(spacing: LoomSpacing.xs) {
                    if agent.hasSession {
                        sessionPill
                    }
                    if agent.isSpawned {
                        spawnPill
                    }
                    if agent.taskCount > 0 {
                        taskCountPill
                    }
                    if let status = agent.pipelineStatus, !status.isEmpty {
                        pipelinePill(status: status)
                    }
                    Spacer()
                    elapsedLabel
                }
            }
        }
        .padding(.vertical, LoomSpacing.xxs)
    }

    // MARK: - Branch Pill

    private var branchPill: some View {
        Text(agent.branch)
            .font(LoomTypography.monoCaption)
            .padding(.horizontal, 5)
            .padding(.vertical, 2)
            .background(LoomColors.accent.opacity(0.1))
            .foregroundStyle(LoomColors.accent)
            .clipShape(Capsule())
            .lineLimit(1)
    }

    // MARK: - Pills

    private var sessionPill: some View {
        HStack(spacing: 3) {
            Image(systemName: "doc.text")
                .font(.system(size: 8))
            Text("\(agent.entryCount) entries")
            if agent.totalTokens > 0 {
                Text("\u{00B7}")
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

    private var taskCountPill: some View {
        HStack(spacing: 3) {
            Image(systemName: "checklist")
                .font(.system(size: 8))
            Text("\(agent.taskCount)")
            if agent.blockedTasks > 0 {
                Text("(\(agent.blockedTasks) blocked)")
                    .foregroundStyle(.orange)
            }
        }
        .font(.caption2)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(Color.gray.opacity(0.1))
        .foregroundStyle(LoomColors.textSecondary)
        .clipShape(Capsule())
    }

    private func pipelinePill(status: String) -> some View {
        let color = pipelineColor(status)
        return HStack(spacing: 3) {
            Image(systemName: "arrow.triangle.2.circlepath")
                .font(.system(size: 8))
            Text("CI \(status)")
        }
        .font(.caption2)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(color.opacity(0.1))
        .foregroundStyle(color)
        .clipShape(Capsule())
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

    // MARK: - Elapsed

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

    // MARK: - Helpers

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
