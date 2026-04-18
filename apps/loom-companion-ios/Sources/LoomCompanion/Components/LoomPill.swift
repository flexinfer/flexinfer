import SwiftUI

/// Unified pill primitive for compact tag/status chips in list rows and metadata strips.
/// Replaces ad-hoc HStack+capsule blocks across AgentRowView, OpsWorkSection, etc.
struct LoomPill: View {
    let text: String
    let icon: String?
    let color: Color
    let style: Style
    let weight: Weight

    enum Style {
        /// Soft tinted background at 12% opacity with full-color foreground. Default.
        case tinted
        /// Transparent background with subtle border. Used for secondary metadata.
        case outlined
        /// Solid color background with inverse foreground. Used for critical emphasis.
        case solid
    }

    enum Weight {
        /// Micro pills inside dense row strips (caption2, 9px padding).
        case micro
        /// Standard pills (caption2, 6/2 padding).
        case compact
    }

    init(
        _ text: String,
        icon: String? = nil,
        color: Color,
        style: Style = .tinted,
        weight: Weight = .compact
    ) {
        self.text = text
        self.icon = icon
        self.color = color
        self.style = style
        self.weight = weight
    }

    var body: some View {
        HStack(spacing: 3) {
            if let icon {
                Image(systemName: icon)
                    .font(.system(size: iconSize))
            }
            Text(text)
                .font(.caption2)
                .fontWeight(.medium)
                .lineLimit(1)
        }
        .padding(.horizontal, horizontalPadding)
        .padding(.vertical, verticalPadding)
        .background(background)
        .foregroundStyle(foreground)
        .overlay(border)
        .clipShape(Capsule())
    }

    private var iconSize: CGFloat {
        weight == .micro ? 8 : 9
    }

    private var horizontalPadding: CGFloat {
        weight == .micro ? 5 : LoomSpacing.pillHorizontalPadding
    }

    private var verticalPadding: CGFloat {
        weight == .micro ? 1.5 : LoomSpacing.pillVerticalPadding
    }

    @ViewBuilder
    private var background: some View {
        switch style {
        case .tinted:
            Capsule().fill(color.opacity(0.12))
        case .outlined:
            Capsule().fill(LoomColors.bgTertiary.opacity(0.4))
        case .solid:
            Capsule().fill(color)
        }
    }

    private var foreground: Color {
        switch style {
        case .tinted: return color
        case .outlined: return LoomColors.textSecondary
        case .solid: return LoomColors.bgPrimary
        }
    }

    @ViewBuilder
    private var border: some View {
        if style == .outlined {
            Capsule().strokeBorder(LoomColors.border, lineWidth: 0.75)
        }
    }
}

#Preview("LoomPill") {
    VStack(spacing: 12) {
        HStack(spacing: 6) {
            LoomPill("live", icon: "bolt.fill", color: LoomColors.statusActive)
            LoomPill("idle", color: LoomColors.statusIdle, style: .outlined)
            LoomPill("blocked", icon: "exclamationmark.triangle.fill", color: LoomColors.statusBlocked)
            LoomPill("5 tasks", icon: "checklist", color: LoomColors.accent)
        }
        HStack(spacing: 6) {
            LoomPill("K8s", icon: "cloud", color: LoomColors.tierShortTerm, weight: .micro)
            LoomPill("feat/ux", icon: "point.3.connected.trianglepath.dotted", color: LoomColors.accent, weight: .micro)
            LoomPill("CI running", icon: "arrow.triangle.2.circlepath", color: LoomColors.statusActive, weight: .micro)
        }
        HStack(spacing: 6) {
            LoomPill("CRITICAL", icon: "exclamationmark.octagon.fill", color: LoomColors.statusCritical, style: .solid)
        }
    }
    .padding()
    .background(LoomColors.bgPrimary)
}
