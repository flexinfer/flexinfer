import SwiftUI

struct PulsingDot: View {
    let color: Color
    let size: CGFloat
    let isPulsing: Bool

    @State private var isAnimating = false

    init(color: Color, size: CGFloat = 8, isPulsing: Bool = true) {
        self.color = color
        self.size = size
        self.isPulsing = isPulsing
    }

    var body: some View {
        ZStack {
            if isPulsing {
                Circle()
                    .fill(color.opacity(0.3))
                    .frame(width: size * 2.5, height: size * 2.5)
                    .scaleEffect(isAnimating ? 1.0 : 0.5)
                    .opacity(isAnimating ? 0.0 : 0.6)
            }

            Circle()
                .fill(color)
                .frame(width: size, height: size)
                .shadow(color: color.opacity(0.5), radius: 3)
        }
        .frame(width: size * 2.5, height: size * 2.5)
        .onAppear {
            guard isPulsing else { return }
            withAnimation(
                .easeInOut(duration: 1.8)
                .repeatForever(autoreverses: false)
            ) {
                isAnimating = true
            }
        }
    }
}

#Preview("PulsingDot") {
    HStack(spacing: 32) {
        VStack {
            PulsingDot(color: .green, isPulsing: true)
            Text("Active").font(.caption)
        }
        VStack {
            PulsingDot(color: .red, isPulsing: true)
            Text("Critical").font(.caption)
        }
        VStack {
            PulsingDot(color: .blue, size: 6, isPulsing: true)
            Text("Unread").font(.caption)
        }
        VStack {
            PulsingDot(color: .gray, isPulsing: false)
            Text("Static").font(.caption)
        }
    }
}
