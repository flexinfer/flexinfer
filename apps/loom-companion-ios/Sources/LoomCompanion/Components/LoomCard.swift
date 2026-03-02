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
                    .fill(.ultraThinMaterial)
                    .overlay {
                        RoundedRectangle(cornerRadius: LoomSpacing.cardCornerRadius, style: .continuous)
                            .strokeBorder(
                                LinearGradient(
                                    colors: [
                                        Color.white.opacity(colorScheme == .dark ? 0.12 : 0.3),
                                        Color.white.opacity(colorScheme == .dark ? 0.04 : 0.1),
                                    ],
                                    startPoint: .topLeading,
                                    endPoint: .bottomTrailing
                                ),
                                lineWidth: 0.5
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
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }

        LoomCard {
            HStack {
                Text("5")
                    .font(LoomTypography.counterLarge)
                Text("Active Agents")
                    .font(LoomTypography.labelLarge)
                    .foregroundStyle(.secondary)
            }
        }
    }
    .padding()
}
