import SwiftUI

struct LoomCard<Content: View>: View {
    let content: Content
    @Environment(\.colorScheme) private var colorScheme

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .padding(LoomSpacing.cardPadding)
            .background {
                RoundedRectangle(cornerRadius: LoomSpacing.cardCornerRadius, style: .continuous)
                    .fill(colorScheme == .dark ? LoomColors.bgSecondary : Color(.systemBackground))
                    .overlay {
                        if colorScheme == .dark {
                            // Top-edge highlight (instrument panel glow)
                            RoundedRectangle(cornerRadius: LoomSpacing.cardCornerRadius, style: .continuous)
                                .fill(
                                    LinearGradient(
                                        colors: [LoomColors.info.opacity(0.05), .clear],
                                        startPoint: .top,
                                        endPoint: .center
                                    )
                                )
                        }
                    }
                    .overlay {
                        RoundedRectangle(cornerRadius: LoomSpacing.cardCornerRadius, style: .continuous)
                            .strokeBorder(
                                colorScheme == .dark
                                    ? LoomColors.border
                                    : Color.white.opacity(0.3),
                                lineWidth: LoomSpacing.cardBorderWidth
                            )
                    }
            }
            .loomCardShadow()
    }
}

extension View {
    func loomCard() -> some View {
        LoomCard { self }
    }
}

#Preview("LoomCard") {
    VStack(spacing: 16) {
        LoomCard {
            VStack(alignment: .leading, spacing: 8) {
                Text("Fleet Health")
                    .font(LoomTypography.headlineMedium)
                Text("All systems operational")
                    .font(LoomTypography.bodyRegular)
                    .foregroundStyle(LoomColors.fgSecondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }

        LoomCard {
            HStack {
                Text("5")
                    .font(LoomTypography.counterLarge)
                Text("Active Agents")
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(LoomColors.fgSecondary)
            }
        }
    }
    .padding()
    .background(LoomColors.bgPrimary)
}
