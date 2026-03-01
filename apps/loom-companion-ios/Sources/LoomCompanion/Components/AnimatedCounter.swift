import SwiftUI

struct AnimatedCounter: View {
    let value: Int
    let font: Font
    let color: Color

    @State private var animatedValue: Int = 0

    init(_ value: Int, font: Font = LoomTypography.counterMedium, color: Color = .primary) {
        self.value = value
        self.font = font
        self.color = color
    }

    var body: some View {
        Text("\(animatedValue)")
            .font(font)
            .foregroundStyle(color)
            .contentTransition(.numericText(value: Double(animatedValue)))
            .onChange(of: value, initial: true) { _, newValue in
                withAnimation(.spring(duration: 0.6, bounce: 0.15)) {
                    animatedValue = newValue
                }
            }
    }
}

#Preview("AnimatedCounter") {
    @Previewable @State var count = 5
    VStack(spacing: 20) {
        AnimatedCounter(count, font: LoomTypography.counterLarge, color: LoomColors.statusHealthy)
        AnimatedCounter(count, font: LoomTypography.counterMedium, color: LoomColors.statusActive)
        AnimatedCounter(count, font: LoomTypography.counterSmall)
        Button("Increment") { count += Int.random(in: 1...10) }
        Button("Reset") { count = 0 }
    }
}
