import SwiftUI

struct SkeletonView: View {
    let width: CGFloat?
    let height: CGFloat
    let cornerRadius: CGFloat

    @State private var shimmerOffset: CGFloat = -1.0

    init(width: CGFloat? = nil, height: CGFloat = 16, cornerRadius: CGFloat = 6) {
        self.width = width
        self.height = height
        self.cornerRadius = cornerRadius
    }

    var body: some View {
        RoundedRectangle(cornerRadius: cornerRadius)
            .fill(LoomColors.bgTertiary)
            .frame(width: width, height: height)
            .overlay {
                RoundedRectangle(cornerRadius: cornerRadius)
                    .fill(
                        LinearGradient(
                            colors: [.clear, Color.white.opacity(0.12), .clear],
                            startPoint: .init(x: shimmerOffset - 0.3, y: 0.5),
                            endPoint: .init(x: shimmerOffset + 0.3, y: 0.5)
                        )
                    )
            }
            .clipShape(RoundedRectangle(cornerRadius: cornerRadius))
            .onAppear {
                withAnimation(
                    .linear(duration: 1.5)
                    .repeatForever(autoreverses: false)
                ) {
                    shimmerOffset = 2.0
                }
            }
    }
}

// MARK: - Pre-composed Skeleton Layouts

/// Matches the hero NextActionCard layout (eyebrow + big headline + subtitle + CTA).
struct SkeletonHeroCard: View {
    var body: some View {
        LoomCard(priority: .hero) {
            VStack(alignment: .leading, spacing: LoomSpacing.md) {
                HStack(spacing: LoomSpacing.xs) {
                    SkeletonView(width: 7, height: 7, cornerRadius: 3.5)
                    SkeletonView(width: 90, height: 10)
                    Spacer()
                    SkeletonView(width: 18, height: 18, cornerRadius: 4)
                }
                SkeletonView(height: 22)
                SkeletonView(width: 220, height: 14)
                HStack {
                    SkeletonView(width: 100, height: 14)
                    Spacer()
                    SkeletonView(width: 60, height: 10)
                }
            }
        }
    }
}

/// Matches the standard attention-lanes / active-work cards (title row + data block).
struct SkeletonDashboardCard: View {
    var body: some View {
        LoomCard(priority: .standard) {
            VStack(alignment: .leading, spacing: LoomSpacing.md) {
                HStack {
                    SkeletonView(width: 120, height: 18)
                    Spacer()
                    SkeletonView(width: 60, height: 14)
                }
                HStack(spacing: LoomSpacing.xl) {
                    ForEach(0..<4, id: \.self) { _ in
                        VStack(spacing: LoomSpacing.xxs) {
                            SkeletonView(width: 32, height: 22)
                            SkeletonView(width: 44, height: 10)
                        }
                    }
                }
            }
        }
    }
}

/// Matches compact context cards (fleet/health when steady — one-line inline).
struct SkeletonCompactRow: View {
    var body: some View {
        LoomCard(priority: .compact) {
            HStack(spacing: LoomSpacing.sm) {
                SkeletonView(width: 14, height: 14, cornerRadius: 7)
                SkeletonView(width: 70, height: 12)
                Spacer()
                SkeletonView(width: 110, height: 6, cornerRadius: 3)
                SkeletonView(width: 40, height: 12)
            }
        }
    }
}

struct SkeletonSessionRow: View {
    var body: some View {
        HStack(spacing: LoomSpacing.sm) {
            SkeletonView(width: 4, height: 48, cornerRadius: 2)
            VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                HStack {
                    SkeletonView(width: 100, height: 14)
                    Spacer()
                    SkeletonView(width: 50, height: 18, cornerRadius: 9)
                }
                SkeletonView(width: 160, height: 12)
                SkeletonView(width: 80, height: 10)
            }
        }
        .padding(.vertical, LoomSpacing.xxs)
    }
}

struct SkeletonAgentRow: View {
    var body: some View {
        HStack(spacing: LoomSpacing.sm) {
            SkeletonView(width: 4, height: 56, cornerRadius: 2)
            VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                HStack {
                    SkeletonView(width: 20, height: 16, cornerRadius: 4)
                    SkeletonView(width: 110, height: 14)
                    Spacer()
                    SkeletonView(width: 56, height: 18, cornerRadius: 9)
                }
                SkeletonView(width: 180, height: 12)
                HStack(spacing: LoomSpacing.sm) {
                    SkeletonView(width: 90, height: 10)
                    SkeletonView(width: 70, height: 10)
                }
                HStack {
                    SkeletonView(width: 80, height: 16, cornerRadius: 8)
                    Spacer()
                    SkeletonView(width: 50, height: 10)
                }
            }
        }
        .padding(.vertical, LoomSpacing.xxs)
    }
}

#Preview("Skeletons") {
    VStack(spacing: 16) {
        SkeletonDashboardCard()
        SkeletonDashboardCard()
        ForEach(0..<3, id: \.self) { _ in
            SkeletonSessionRow()
        }
    }
    .padding()
}
