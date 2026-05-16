import SwiftUI

/// Canonical empty state matching the HUD's triage-overview pattern (Slice A2):
/// a centered status dot, a short sans headline, and a mono detail line. Use
/// this everywhere a list, deck, or surface is **intentionally clear** — not
/// for first-load skeletons (use `Skeleton*`) and not for errors (use the
/// existing `ContentUnavailableView` error pattern).
///
/// Tone maps to the dot color so the same primitive reads as "all good"
/// (`.nominal`) or "nothing yet — here's how to start" (`.idle`).
struct LoomEmptyState<Action: View>: View {
    let tone: Tone
    let title: String
    let detail: String?
    @ViewBuilder let action: () -> Action

    enum Tone {
        /// "System nominal" — green dot. Use when the surface should be empty
        /// because the world is healthy (no pressure points, no alerts).
        case nominal
        /// "Nothing here yet" — muted dot. Use when the surface is empty
        /// because the operator hasn't started anything (no sessions, no agents).
        case idle
        /// Warning dot. Use for "loaded successfully but the result is a
        /// surprising zero" — almost never the right choice.
        case attention

        var color: Color {
            switch self {
            case .nominal: return LoomColors.statusHealthy
            case .idle: return LoomColors.fgMuted
            case .attention: return LoomColors.statusDegraded
            }
        }
    }

    init(
        tone: Tone = .nominal,
        title: String,
        detail: String? = nil,
        @ViewBuilder action: @escaping () -> Action = { EmptyView() }
    ) {
        self.tone = tone
        self.title = title
        self.detail = detail
        self.action = action
    }

    var body: some View {
        VStack(spacing: LoomSpacing.md) {
            // Centered dot with soft glow ring — same visual language as
            // PulsingDot but stationary. The HUD A2 spec uses a U+00B7 middle
            // dot here; SwiftUI gets us closer with a real shape + glow.
            ZStack {
                Circle()
                    .fill(tone.color.opacity(0.12))
                    .frame(width: 28, height: 28)
                Circle()
                    .fill(tone.color)
                    .frame(width: 10, height: 10)
                    .shadow(color: tone.color.opacity(0.55), radius: 6)
            }

            VStack(spacing: LoomSpacing.xxs) {
                Text(title)
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.fgPrimary)
                    .multilineTextAlignment(.center)

                if let detail, !detail.isEmpty {
                    Text(detail)
                        .font(LoomTypography.monoSmall)
                        .foregroundStyle(LoomColors.fgSecondary)
                        .multilineTextAlignment(.center)
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            let actionContent = action()
            if !(actionContent is EmptyView) {
                actionContent
                    .padding(.top, LoomSpacing.xs)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, LoomSpacing.xxl)
        .padding(.horizontal, LoomSpacing.lg)
    }
}

// Convenience overload for the common case (no action).
extension LoomEmptyState where Action == EmptyView {
    init(tone: Tone = .nominal, title: String, detail: String? = nil) {
        self.init(tone: tone, title: title, detail: detail) { EmptyView() }
    }
}

#Preview("LoomEmptyState · tones") {
    ScrollView {
        VStack(spacing: LoomSpacing.lg) {
            LoomEmptyState(
                tone: .nominal,
                title: "System nominal",
                detail: "No pressure points. 4 agents active · 0 blocked tasks."
            )
            .loomCard(priority: .standard)

            LoomEmptyState(
                tone: .idle,
                title: "No sessions yet",
                detail: "Agent sessions appear here when coding agents connect.\nStart a session from the Work tab."
            ) {
                Button {
                } label: {
                    Label("Create session", systemImage: "plus.circle")
                        .font(LoomTypography.labelLarge)
                }
                .buttonStyle(.borderedProminent)
                .tint(LoomColors.accent)
            }
            .loomCard(priority: .standard)

            LoomEmptyState(
                tone: .attention,
                title: "No matching agents",
                detail: "Filter cleared 12 agents · adjust criteria"
            )
            .loomCard(priority: .compact)
        }
        .padding()
    }
    .background(LoomColors.bgPrimary)
    .preferredColorScheme(.dark)
}
