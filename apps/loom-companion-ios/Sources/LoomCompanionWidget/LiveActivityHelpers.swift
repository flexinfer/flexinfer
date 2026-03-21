import SwiftUI

// MARK: - Shared helpers for Live Activity views

/// Return an SF Symbol name for the given agent type.
func agentIcon(_ agentType: String) -> String {
    switch agentType.lowercased() {
    case "claude-code", "claude": return "terminal.fill"
    case "gemini": return "wand.and.sparkles"
    case "codex": return "chevron.left.forwardslash.chevron.right"
    case "kilocode": return "ruler.fill"
    case "antigravity": return "arrow.up.circle.fill"
    default: return "cpu.fill"
    }
}

/// Return a brand color for the given agent type.
func agentColor(_ agentType: String) -> Color {
    switch agentType.lowercased() {
    case "claude-code", "claude": return Color(red: 0.85, green: 0.55, blue: 0.25)
    case "gemini": return Color(red: 0.3, green: 0.65, blue: 0.95)
    case "codex": return Color(red: 0.4, green: 0.8, blue: 0.4)
    case "kilocode": return Color(red: 0.7, green: 0.4, blue: 0.9)
    case "antigravity": return Color(red: 0.95, green: 0.4, blue: 0.4)
    default: return .indigo
    }
}

/// Return an SF Symbol name representing the session status.
func statusDot(_ status: String) -> String {
    switch status {
    case "active": return "circle.fill"
    case "idle": return "circle.dotted"
    case "ended", "summarized": return "checkmark.circle.fill"
    case "error", "failed": return "exclamationmark.circle.fill"
    default: return "circle"
    }
}

/// Return a color for the given session status.
func statusDotColor(_ status: String) -> Color {
    switch status {
    case "active": return .green
    case "idle": return .orange
    case "ended": return .gray
    case "summarized": return .blue
    case "error", "failed": return .red
    default: return .secondary
    }
}

/// Format a token count for compact display (e.g., 1200 -> "1.2k").
func formatTokens(_ count: Int) -> String {
    if count >= 1000 {
        return String(format: "%.1fk", Double(count) / 1000.0)
    }
    return "\(count)"
}
