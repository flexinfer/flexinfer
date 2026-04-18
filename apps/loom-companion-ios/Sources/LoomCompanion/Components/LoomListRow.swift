import SwiftUI

/// Unified list-row primitive that establishes clear visual hierarchy:
/// status-accent-bar → leading slot (icon) → primary lane (title + metric) →
/// secondary lane (subtitle) → optional footer lane (pills).
///
/// - Live rows emphasize the accent bar (prominent width + pulse) and subtly
///   gradient-tint the left edge of the content.
/// - Attention rows add a warning symbol to the trailing side.
/// - Critical rows promote the accent bar and can use a solid pill in the
///   primary lane to call out the issue.
///
/// Adopt this instead of hand-rolling HStack/VStack row layouts so every
/// list surface communicates the same visual language.
struct LoomListRow<Leading: View, Trailing: View, Footer: View>: View {
    let accentColor: Color
    let isLive: Bool
    let needsAttention: Bool
    let emphasizeTitle: Bool
    let title: String
    let subtitle: String?

    @ViewBuilder let leading: () -> Leading
    @ViewBuilder let trailing: () -> Trailing
    @ViewBuilder let footer: () -> Footer

    init(
        accentColor: Color,
        title: String,
        subtitle: String? = nil,
        isLive: Bool = false,
        needsAttention: Bool = false,
        emphasizeTitle: Bool = false,
        @ViewBuilder leading: @escaping () -> Leading = { EmptyView() },
        @ViewBuilder trailing: @escaping () -> Trailing = { EmptyView() },
        @ViewBuilder footer: @escaping () -> Footer = { EmptyView() }
    ) {
        self.accentColor = accentColor
        self.title = title
        self.subtitle = subtitle
        self.isLive = isLive
        self.needsAttention = needsAttention
        self.emphasizeTitle = emphasizeTitle
        self.leading = leading
        self.trailing = trailing
        self.footer = footer
    }

    var body: some View {
        HStack(spacing: LoomSpacing.rowContentSpacing) {
            StatusAccentBar(color: accentColor, isLive: isLive, prominent: isLive || emphasizeTitle)
                .frame(minHeight: LoomSpacing.rowMinHeight)
                // Animate bar width change when a row goes live mid-session
                .animation(.spring(duration: 0.35, bounce: 0.2), value: isLive)

            VStack(alignment: .leading, spacing: LoomSpacing.xxs) {
                primaryLane
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                        .lineLimit(1)
                        .contentTransition(.interpolate)
                }
                footerLane
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, LoomSpacing.xxs)
        .background(liveTint, alignment: .leading)
        .animation(.easeInOut(duration: 0.4), value: isLive)
        // Flash accent briefly when the row's status shifts. Compares the
        // hashValue of the accent color so callers don't need to thread a
        // discrete status token.
        .loomValueChangeFlash(accentColorHash, color: accentColor)
        .contentShape(Rectangle())
    }

    private var accentColorHash: Int {
        // Color isn't Hashable in a stable way across SwiftUI versions, but
        // using its string description gives us a stable token for the flash
        // modifier to detect changes.
        String(describing: accentColor).hashValue
    }

    @ViewBuilder
    private var primaryLane: some View {
        HStack(spacing: LoomSpacing.xs) {
            leading()

            Text(title)
                .font(emphasizeTitle ? LoomTypography.headlineMedium : LoomTypography.bodyMedium)
                .foregroundStyle(LoomColors.fgPrimary)
                .lineLimit(1)
                .truncationMode(.tail)

            Spacer(minLength: 4)

            if needsAttention {
                Image(systemName: "exclamationmark.circle.fill")
                    .font(.system(size: 12))
                    .foregroundStyle(LoomColors.statusDegraded)
                    .accessibilityLabel("Needs attention")
            }

            trailing()
        }
    }

    @ViewBuilder
    private var footerLane: some View {
        let footerContent = footer()
        if !(footerContent is EmptyView) {
            HStack(spacing: LoomSpacing.xs) {
                footerContent
            }
            .padding(.top, 1)
            .transition(.loomPillAppear)
        }
    }

    /// Subtle horizontal gradient bleeding from the accent bar into the row
    /// content to further signal live status without shouting.
    @ViewBuilder
    private var liveTint: some View {
        if isLive {
            LinearGradient(
                colors: [accentColor.opacity(0.10), accentColor.opacity(0.0)],
                startPoint: .leading,
                endPoint: .trailing
            )
            .frame(maxWidth: 160)
            .allowsHitTesting(false)
        }
    }
}

// MARK: - Convenience: Icon Leading Slot

/// Compact icon view matching the row visual system: small circle-bg + symbol.
/// Use inside `leading: { LoomRowIcon(systemName: ..., color: ...) }`.
struct LoomRowIcon: View {
    let systemName: String
    let color: Color
    let size: CGFloat

    init(systemName: String, color: Color, size: CGFloat = 11) {
        self.systemName = systemName
        self.color = color
        self.size = size
    }

    var body: some View {
        Image(systemName: systemName)
            .font(.system(size: size, weight: .semibold))
            .foregroundStyle(color)
            .frame(width: 16, height: 16)
            .background(
                Circle().fill(color.opacity(0.12))
            )
    }
}

// MARK: - Convenience: Metric Trailing Slot

/// One-key-metric trailing slot. Mono font, right-aligned, accent color optional.
struct LoomRowMetric: View {
    let value: String
    let unit: String?
    let color: Color

    init(_ value: String, unit: String? = nil, color: Color = LoomColors.textSecondary) {
        self.value = value
        self.unit = unit
        self.color = color
    }

    var body: some View {
        HStack(spacing: 2) {
            Text(value)
                .font(LoomTypography.monoMedium)
                .foregroundStyle(color)
            if let unit {
                Text(unit)
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(color.opacity(0.7))
            }
        }
    }
}

#Preview("LoomListRow · states") {
    VStack(spacing: 0) {
        LoomListRow(
            accentColor: LoomColors.statusHealthy,
            title: "claude-code",
            subtitle: "services/loom-core · feat/ux-craft",
            isLive: true,
            emphasizeTitle: true,
            leading: { LoomRowIcon(systemName: "terminal.fill", color: Color(red: 1.000, green: 0.420, blue: 0.616)) },
            trailing: { LoomRowMetric("12.4k", unit: "tok", color: LoomColors.statusHealthy) },
            footer: {
                LoomPill("live", icon: "bolt.fill", color: LoomColors.statusActive, weight: .micro)
                LoomPill("42 entries", color: LoomColors.accent, weight: .micro)
            }
        )
        Divider().overlay(LoomColors.border)
        LoomListRow(
            accentColor: LoomColors.statusDegraded,
            title: "codex",
            subtitle: "platform/gitops · idle 2m",
            needsAttention: true,
            leading: { LoomRowIcon(systemName: "chevron.left.forwardslash.chevron.right", color: LoomColors.statusHealthy) },
            trailing: { LoomRowMetric("820", unit: "tok") },
            footer: {
                LoomPill("idle", color: LoomColors.statusIdle, style: .outlined, weight: .micro)
                LoomPill("2 blocked", icon: "exclamationmark.triangle.fill", color: LoomColors.statusBlocked, weight: .micro)
            }
        )
        Divider().overlay(LoomColors.border)
        LoomListRow(
            accentColor: LoomColors.statusCritical,
            title: "pipeline failed · main",
            subtitle: "lint stage · 12m ago",
            emphasizeTitle: true,
            leading: { LoomRowIcon(systemName: "xmark.octagon.fill", color: LoomColors.statusCritical) },
            trailing: { LoomRowMetric("3", unit: "errs", color: LoomColors.statusCritical) },
            footer: {
                LoomPill("CI failed", icon: "xmark.circle.fill", color: LoomColors.statusCritical, style: .solid, weight: .micro)
            }
        )
        Divider().overlay(LoomColors.border)
        LoomListRow(
            accentColor: LoomColors.statusIdle,
            title: "gemini-cli",
            subtitle: "offline · last seen 1h ago",
            leading: { LoomRowIcon(systemName: "wand.and.sparkles", color: LoomColors.fgMuted) },
            trailing: { LoomRowMetric("—") }
        )
    }
    .padding()
    .background(LoomColors.bgPrimary)
}
