import SwiftUI
import LoomCompanionKit

enum LoomColors {
    // MARK: - Semantic Status

    static let statusHealthy = Color.green
    static let statusDegraded = Color.orange
    static let statusCritical = Color.red
    static let statusIdle = Color.gray
    static let statusActive = Color.blue
    static let statusBlocked = Color(red: 0.9, green: 0.35, blue: 0.25) // warm red — distinct from critical
    static let statusInfo = Color.indigo

    // MARK: - Memory Tier Palette (aligned with HUD design tokens)

    static let tierWorking = Color(red: 0.31, green: 0.92, blue: 0.99)   // ~#4EEAFE
    static let tierShortTerm = Color(red: 0.61, green: 0.36, blue: 0.82) // ~#9B5CD0
    static let tierLongTerm = Color.green                                  // matches statusHealthy

    // MARK: - Accent

    static let accent = Color.indigo

    // MARK: - Text

    static let textPrimary = Color.primary
    static let textSecondary = Color.secondary
    static let textTertiary = Color.secondary.opacity(0.6)

    // MARK: - Surface Overlays

    static let cardBorderLight = Color.white.opacity(0.12)
    static let cardBorderDark = Color.white.opacity(0.04)

    // MARK: - Severity Backgrounds

    static func severityBackground(_ severity: AlertSeverity) -> Color {
        switch severity {
        case .critical: return statusCritical.opacity(0.12)
        case .warning: return statusDegraded.opacity(0.10)
        case .info: return statusInfo.opacity(0.08)
        }
    }

    // MARK: - Session Status Color

    static func sessionStatusColor(_ status: SessionStatus) -> Color {
        switch status {
        case .active: return statusHealthy
        case .ended: return textTertiary
        case .summarized: return statusActive
        case .unknown: return statusIdle
        }
    }

    // MARK: - Health Status Color

    static func healthStatusColor(_ status: OverallHealthStatus) -> Color {
        switch status {
        case .healthy: return statusHealthy
        case .degraded: return statusDegraded
        case .critical: return statusCritical
        case .unknown: return statusIdle
        }
    }

    // MARK: - Presence Status Color

    static func presenceStatusColor(_ status: MobilePresenceStatus) -> Color {
        switch status {
        case .active: return statusHealthy
        case .idle: return statusDegraded
        case .offline: return statusIdle
        case .unknown: return statusIdle
        }
    }

    // MARK: - Agent Type Color

    static func agentTypeColor(_ type: String) -> Color {
        switch type.lowercased() {
        case "claude-code", "claude": return Color(red: 0.91, green: 0.36, blue: 0.45) // #E95D74
        case "gemini": return Color(red: 0.00, green: 0.53, blue: 0.60)                // #018799
        case "codex": return Color(red: 0.13, green: 0.70, blue: 0.33)                 // #22B255
        case "copilot": return Color(red: 0.91, green: 0.70, blue: 0.07)               // #E7B312
        case "kilocode": return Color(red: 0.70, green: 0.40, blue: 0.90)              // purple
        case "antigravity": return Color(red: 0.95, green: 0.40, blue: 0.40)           // coral
        default: return statusInfo
        }
    }

    // MARK: - Agent Type Icon

    static func agentTypeIcon(_ type: String) -> String {
        switch type.lowercased() {
        case "claude-code", "claude": return "terminal.fill"
        case "gemini": return "wand.and.sparkles"
        case "codex": return "chevron.left.forwardslash.chevron.right"
        case "kilocode": return "ruler.fill"
        case "antigravity": return "arrow.up.circle.fill"
        default: return "cpu.fill"
        }
    }
}
