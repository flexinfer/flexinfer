import SwiftUI

// MARK: - Card Appear Animation (staggered scale + fade)

struct CardAppearModifier: ViewModifier {
    let index: Int
    @State private var isVisible = false

    func body(content: Content) -> some View {
        content
            .opacity(isVisible ? 1 : 0)
            .scaleEffect(isVisible ? 1 : 0.92)
            .offset(y: isVisible ? 0 : 12)
            .onAppear {
                withAnimation(
                    .spring(duration: 0.5, bounce: 0.12)
                    .delay(Double(index) * 0.08)
                ) {
                    isVisible = true
                }
            }
    }
}

extension View {
    func cardAppear(index: Int) -> some View {
        modifier(CardAppearModifier(index: index))
    }
}

// MARK: - Status Change Animation

struct StatusChangeModifier: ViewModifier {
    let status: String
    @State private var bounce = false

    func body(content: Content) -> some View {
        content
            .scaleEffect(bounce ? 1.08 : 1.0)
            .onChange(of: status) { _, _ in
                withAnimation(.spring(duration: 0.3, bounce: 0.4)) {
                    bounce = true
                }
                withAnimation(.spring(duration: 0.3, bounce: 0.1).delay(0.15)) {
                    bounce = false
                }
            }
    }
}

extension View {
    func animateOnStatusChange(_ status: String) -> some View {
        modifier(StatusChangeModifier(status: status))
    }
}

// MARK: - Slide In From Top (for ErrorBanner, toasts)

extension AnyTransition {
    static var slideInFromTop: AnyTransition {
        .asymmetric(
            insertion: .move(edge: .top).combined(with: .opacity),
            removal: .move(edge: .top).combined(with: .opacity)
        )
    }
}
