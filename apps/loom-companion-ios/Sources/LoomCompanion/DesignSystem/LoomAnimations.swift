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

// MARK: - Pulse Animation (for Live Activity indicators)

struct PulseModifier: ViewModifier {
    @State private var isPulsing = false

    func body(content: Content) -> some View {
        content
            .opacity(isPulsing ? 0.4 : 1.0)
            .onAppear {
                withAnimation(
                    .easeInOut(duration: 1.2)
                    .repeatForever(autoreverses: true)
                ) {
                    isPulsing = true
                }
            }
    }
}

extension View {
    func pulse() -> some View {
        modifier(PulseModifier())
    }
}

// MARK: - Count Up Animation (for animated number transitions)

struct CountUpModifier: AnimatableModifier {
    var value: Double

    var animatableData: Double {
        get { value }
        set { value = newValue }
    }

    func body(content: Content) -> some View {
        Text("\(Int(value))")
    }
}

extension View {
    func animatedCount(_ value: Int) -> some View {
        modifier(CountUpModifier(value: Double(value)))
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

    /// Used for pills and chips that appear mid-row when a state begins.
    /// Scales from 0.8, fades in, moves a hair — reads as "this just happened"
    /// rather than a generic fade.
    static var loomPillAppear: AnyTransition {
        .asymmetric(
            insertion: .scale(scale: 0.8, anchor: .leading)
                .combined(with: .opacity)
                .combined(with: .move(edge: .leading)),
            removal: .scale(scale: 0.8, anchor: .leading)
                .combined(with: .opacity)
        )
    }
}

// MARK: - Value-Change Flash (communicates "this just changed")
//
// When a semantic value changes (status, count, severity), briefly brighten
// a wrapping color tint. Decays quickly so it reads as acknowledgment, not
// ambient noise. Use sparingly — every flash trains the eye to look here.

struct ValueChangeFlashModifier<V: Equatable>: ViewModifier {
    let value: V
    let color: Color
    @State private var flashOn = false

    func body(content: Content) -> some View {
        content
            .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(color.opacity(flashOn ? 0.22 : 0))
                    .allowsHitTesting(false)
                    .animation(.easeOut(duration: 0.7), value: flashOn)
            )
            .onChange(of: value) { _, _ in
                flashOn = true
                Task { @MainActor in
                    try? await Task.sleep(nanoseconds: 50_000_000)
                    flashOn = false
                }
            }
    }
}

extension View {
    /// Flashes a colored overlay briefly when `value` changes. Perfect for
    /// rows that transition status (pending → in-progress, idle → active).
    func loomValueChangeFlash<V: Equatable>(_ value: V, color: Color) -> some View {
        modifier(ValueChangeFlashModifier(value: value, color: color))
    }
}

// MARK: - Tap Feedback (subtle press response distinct from navigation push)

struct TapFeedbackModifier: ViewModifier {
    @State private var pressed = false
    let haptic: Bool

    func body(content: Content) -> some View {
        content
            .scaleEffect(pressed ? 0.98 : 1.0)
            .animation(.interactiveSpring(response: 0.25, dampingFraction: 0.7), value: pressed)
            .onLongPressGesture(minimumDuration: 0.01, maximumDistance: 50) {
                // Tap ended
            } onPressingChanged: { isPressing in
                pressed = isPressing
                if isPressing && haptic {
                    HapticManager.light()
                }
            }
    }
}

extension View {
    /// Adds a subtle press-scale + optional haptic. Distinguishes a touch from
    /// accidental scroll momentum without the heavy look of `.buttonStyle(.plain)`.
    func loomTapFeedback(haptic: Bool = true) -> some View {
        modifier(TapFeedbackModifier(haptic: haptic))
    }
}
