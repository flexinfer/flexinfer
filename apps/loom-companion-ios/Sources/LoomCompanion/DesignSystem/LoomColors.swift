import SwiftUI
import LoomCompanionKit

enum LoomColors {
    // MARK: - Background Surfaces (dark-ocean palette)

    static let bgPrimary = Color(red: 0.024, green: 0.047, blue: 0.063)     // #060C10
    static let bgSecondary = Color(red: 0.047, green: 0.082, blue: 0.098)   // #0C1519
    static let bgTertiary = Color(red: 0.075, green: 0.122, blue: 0.145)    // #131F25
    static let bgElevated = Color(red: 0.102, green: 0.169, blue: 0.200)    // #1A2B33

    // MARK: - Foreground / Text

    static let fgPrimary = Color(red: 0.831, green: 0.933, blue: 0.957)     // #D4EEF4
    static let fgSecondary = Color(red: 0.427, green: 0.667, blue: 0.722)   // #6DAAB8
    static let fgMuted = Color(red: 0.180, green: 0.400, blue: 0.451)       // #2E6673
    static let fgDim = Color(red: 0.102, green: 0.271, blue: 0.314)         // #1A4550

    // MARK: - Text Aliases (backward-compatible)

    static let textPrimary = fgPrimary
    static let textSecondary = fgSecondary
    static let textTertiary = fgMuted

    // MARK: - Borders

    static let border = Color(red: 0.082, green: 0.165, blue: 0.196)        // #152A32
    static let borderSubtle = Color(red: 0.059, green: 0.122, blue: 0.149)  // #0F1F26

    // MARK: - Semantic Status

    static let statusHealthy = Color(red: 0.133, green: 0.878, blue: 0.463)  // #22E076
    static let statusDegraded = Color(red: 1.000, green: 0.722, blue: 0.188) // #FFB830
    static let statusCritical = Color(red: 1.000, green: 0.239, blue: 0.443) // #FF3D71
    static let statusIdle = fgMuted                                           // #2E6673
    static let statusActive = Color(red: 0.000, green: 0.784, blue: 1.000)   // #00C8FF
    static let statusBlocked = Color(red: 0.9, green: 0.35, blue: 0.25)      // warm red — distinct from critical
    static let statusInfo = Color(red: 0.000, green: 0.784, blue: 1.000)     // #00C8FF

    // MARK: - Interactive / Accent

    static let accent = Color(red: 1.000, green: 0.420, blue: 0.208)         // #FF6B35 signal orange
    static let info = Color(red: 0.000, green: 0.784, blue: 1.000)           // #00C8FF primary interactive

    // MARK: - Dim Tokens (status color at 10-12% opacity for backgrounds)

    static let accentDim = accent.opacity(0.12)
    static let infoDim = info.opacity(0.10)
    static let successDim = statusHealthy.opacity(0.10)
    static let errorDim = statusCritical.opacity(0.10)
    static let warningDim = statusDegraded.opacity(0.10)

    // MARK: - Memory Tier Palette

    static let tierWorking = Color(red: 0.31, green: 0.92, blue: 0.99)       // #4EEAFE
    static let tierShortTerm = Color(red: 0.61, green: 0.36, blue: 0.82)     // #9B5CD0
    static let tierLongTerm = statusHealthy                                    // matches healthy

    // MARK: - Surface Overlays

    static let cardBorderLight = Color.white.opacity(0.12)
    static let cardBorderDark = Color.white.opacity(0.04)

    // MARK: - Severity Backgrounds

    static func severityBackground(_ severity: AlertSeverity) -> Color {
        switch severity {
        case .critical: return errorDim
        case .warning: return warningDim
        case .info: return infoDim
        }
    }

    // MARK: - Session Status Color

    static func sessionStatusColor(_ status: SessionStatus) -> Color {
        switch status {
        case .active: return statusHealthy
        case .ended: return fgMuted
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
        case "claude-code", "claude": return Color(red: 1.000, green: 0.420, blue: 0.616)  // #FF6B9D
        case "gemini": return Color(red: 0.000, green: 0.784, blue: 1.000)                 // #00C8FF
        case "codex": return Color(red: 0.133, green: 0.878, blue: 0.463)                  // #22E076
        case "copilot": return Color(red: 1.000, green: 0.722, blue: 0.188)                // #FFB830
        case "kilocode": return Color(red: 0.690, green: 0.424, blue: 0.871)               // #B06CDE
        case "antigravity": return Color(red: 1.000, green: 0.420, blue: 0.208)            // #FF6B35
        default: return info
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
