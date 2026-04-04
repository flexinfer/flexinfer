import SwiftUI

private struct CardShadowModifier: ViewModifier {
    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        content.shadow(
            color: colorScheme == .dark
                ? Color(red: 0.02, green: 0.04, blue: 0.06).opacity(0.5)
                : Color.black.opacity(0.08),
            radius: 8,
            x: 0,
            y: 4
        )
    }
}

private struct ElevatedShadowModifier: ViewModifier {
    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        content.shadow(
            color: colorScheme == .dark
                ? Color(red: 0.02, green: 0.04, blue: 0.06).opacity(0.7)
                : Color.black.opacity(0.15),
            radius: 16,
            x: 0,
            y: 8
        )
    }
}

private struct StatusGlowModifier: ViewModifier {
    let color: Color

    func body(content: Content) -> some View {
        content.shadow(color: color.opacity(0.18), radius: 6, x: 0, y: 0)
    }
}

extension View {
    func loomCardShadow() -> some View {
        modifier(CardShadowModifier())
    }

    func loomElevatedShadow() -> some View {
        modifier(ElevatedShadowModifier())
    }

    func loomStatusGlow(_ color: Color) -> some View {
        modifier(StatusGlowModifier(color: color))
    }
}
