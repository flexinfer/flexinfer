import SwiftUI
import LoomCompanionKit

/// Small pulsing banner shown at the top of views when Live Activities are active.
/// Shows a breakdown of activity types (sessions, workflows, pipelines). Tap to navigate.
struct LiveActivityBanner: View {
    let sessionCount: Int
    let workflowCount: Int
    let pipelineCount: Int
    var onTap: (() -> Void)?

    /// Convenience init with just a total count (backward-compatible).
    init(activeCount: Int, onTap: (() -> Void)? = nil) {
        self.sessionCount = activeCount
        self.workflowCount = 0
        self.pipelineCount = 0
        self.onTap = onTap
    }

    /// Full init with per-type counts.
    init(sessionCount: Int, workflowCount: Int, pipelineCount: Int, onTap: (() -> Void)? = nil) {
        self.sessionCount = sessionCount
        self.workflowCount = workflowCount
        self.pipelineCount = pipelineCount
        self.onTap = onTap
    }

    private var totalCount: Int {
        sessionCount + workflowCount + pipelineCount
    }

    var body: some View {
        if totalCount > 0 {
            Button(action: {
                #if os(iOS)
                HapticManager.light()
                #endif
                onTap?()
            }) {
                HStack(spacing: 8) {
                    Circle()
                        .fill(.green)
                        .frame(width: 8, height: 8)
                        .pulse()

                    activitySummary

                    Spacer()

                    Image(systemName: "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .background(.ultraThinMaterial)
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }
            .buttonStyle(.plain)
            .transition(.slideInFromTop)
            .accessibilityLabel("\(totalCount) Live \(totalCount == 1 ? "Activity" : "Activities") active")
        }
    }

    @ViewBuilder
    private var activitySummary: some View {
        if workflowCount == 0 && pipelineCount == 0 {
            // Simple mode: just show total
            Text("\(totalCount) Live \(totalCount == 1 ? "Activity" : "Activities")")
                .font(.caption)
                .fontWeight(.medium)
        } else {
            // Detailed mode: show per-type breakdown
            HStack(spacing: 6) {
                if sessionCount > 0 {
                    Label("\(sessionCount)", systemImage: "terminal.fill")
                        .font(.caption2)
                        .fontWeight(.medium)
                }
                if workflowCount > 0 {
                    Label("\(workflowCount)", systemImage: "arrow.triangle.branch")
                        .font(.caption2)
                        .fontWeight(.medium)
                }
                if pipelineCount > 0 {
                    Label("\(pipelineCount)", systemImage: "server.rack")
                        .font(.caption2)
                        .fontWeight(.medium)
                }
            }
        }
    }
}
