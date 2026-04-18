import SwiftUI

/// Card priority governs visual scale and emphasis:
/// - `.hero`   — the single most important thing on the surface. Larger padding,
///                stronger top-edge glow, thicker border. Use for the primary CTA
///                / next action. At most one hero per surface.
/// - `.standard` — default attention cards (active work, attention lanes).
/// - `.compact`  — supporting context that should recede when nothing is wrong
///                (fleet/health when steady state). Tighter padding, no glow.
enum LoomCardPriority {
    case hero
    case standard
    case compact
}

/// Card accent lets a card take on a semantic tint — used by hero cards to
/// telegraph severity (critical/warning/info) without pulling attention into
/// the body. The accent appears as a subtle left-edge vertical gradient plus
/// a stronger top-edge glow.
struct LoomCardAccent {
    let color: Color
    let pulse: Bool

    static let none = LoomCardAccent(color: .clear, pulse: false)
    static func severity(_ color: Color, pulse: Bool = false) -> LoomCardAccent {
        LoomCardAccent(color: color, pulse: pulse)
    }
}

struct LoomCard<Content: View>: View {
    let priority: LoomCardPriority
    let accent: LoomCardAccent
    let content: Content

    @Environment(\.colorScheme) private var colorScheme
    @State private var pulseOn = false

    init(
        priority: LoomCardPriority = .standard,
        accent: LoomCardAccent = .none,
        @ViewBuilder content: () -> Content
    ) {
        self.priority = priority
        self.accent = accent
        self.content = content()
    }

    private var padding: CGFloat {
        switch priority {
        case .hero: return LoomSpacing.lg
        case .standard: return LoomSpacing.cardPadding
        case .compact: return LoomSpacing.md
        }
    }

    private var cornerRadius: CGFloat {
        switch priority {
        case .hero: return 18
        case .standard: return LoomSpacing.cardCornerRadius
        case .compact: return 12
        }
    }

    private var borderWidth: CGFloat {
        switch priority {
        case .hero: return 1.25
        case .standard: return LoomSpacing.cardBorderWidth
        case .compact: return 0.75
        }
    }

    private var borderColor: Color {
        guard colorScheme == .dark else { return Color.white.opacity(0.3) }
        switch priority {
        case .hero:
            return accent.color == .clear
                ? LoomColors.border
                : accent.color.opacity(pulseOn ? 0.55 : 0.38)
        case .standard: return LoomColors.border
        case .compact: return LoomColors.borderSubtle
        }
    }

    /// Top-edge glow intensity scales with priority.
    private var topGlowOpacity: Double {
        guard colorScheme == .dark else { return 0 }
        switch priority {
        case .hero: return pulseOn && accent.pulse ? 0.22 : 0.14
        case .standard: return 0.05
        case .compact: return 0
        }
    }

    private var topGlowColor: Color {
        accent.color == .clear ? LoomColors.info : accent.color
    }

    var body: some View {
        content
            .padding(padding)
            .background {
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .fill(backgroundColor)
                    .overlay(topGlow)
                    .overlay(
                        RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                            .strokeBorder(borderColor, lineWidth: borderWidth)
                    )
            }
            .loomCardShadow()
            .onAppear {
                guard accent.pulse else { return }
                withAnimation(.easeInOut(duration: 1.6).repeatForever(autoreverses: true)) {
                    pulseOn = true
                }
            }
    }

    private var backgroundColor: Color {
        if colorScheme == .dark {
            switch priority {
            case .hero: return LoomColors.bgSecondary
            case .standard: return LoomColors.bgSecondary
            case .compact: return LoomColors.bgSecondary.opacity(0.55)
            }
        }
        return Color(.systemBackground)
    }

    @ViewBuilder
    private var topGlow: some View {
        if topGlowOpacity > 0 {
            RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                .fill(
                    LinearGradient(
                        colors: [topGlowColor.opacity(topGlowOpacity), .clear],
                        startPoint: .top,
                        endPoint: .center
                    )
                )
                .allowsHitTesting(false)
        }
    }

}

extension View {
    func loomCard(priority: LoomCardPriority = .standard, accent: LoomCardAccent = .none) -> some View {
        LoomCard(priority: priority, accent: accent) { self }
    }
}

#Preview("LoomCard priorities") {
    ScrollView {
        VStack(spacing: 16) {
            LoomCard(priority: .hero, accent: .severity(LoomColors.statusCritical, pulse: true)) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("NEXT")
                        .font(LoomTypography.sectionTitle)
                        .foregroundStyle(LoomColors.statusCritical)
                    Text("2 servers down — investigate control plane")
                        .font(LoomTypography.headlineLarge)
                        .foregroundStyle(LoomColors.fgPrimary)
                    Text("k8s-harv-01 and k8s-k3s-03 failing health probes for 3m")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.fgSecondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            LoomCard(priority: .standard) {
                HStack {
                    Text("Attention Lanes")
                        .font(LoomTypography.headlineMedium)
                        .foregroundStyle(LoomColors.fgPrimary)
                    Spacer()
                    Text("3 lanes")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.fgSecondary)
                }
            }
            LoomCard(priority: .compact) {
                HStack {
                    Text("Fleet Overview")
                        .font(LoomTypography.labelLarge)
                        .foregroundStyle(LoomColors.fgSecondary)
                    Spacer()
                    Text("12 active · 4 idle")
                        .font(LoomTypography.monoSmall)
                        .foregroundStyle(LoomColors.fgMuted)
                }
            }
        }
        .padding()
    }
    .background(LoomColors.bgPrimary)
    .preferredColorScheme(.dark)
}
