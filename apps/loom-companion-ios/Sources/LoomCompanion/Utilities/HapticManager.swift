import SwiftUI

#if os(iOS)
import UIKit

enum HapticManager {
    static func light() {
        UIImpactFeedbackGenerator(style: .light).impactOccurred()
    }

    static func medium() {
        UIImpactFeedbackGenerator(style: .medium).impactOccurred()
    }

    static func heavy() {
        UIImpactFeedbackGenerator(style: .heavy).impactOccurred()
    }

    static func success() {
        UINotificationFeedbackGenerator().notificationOccurred(.success)
    }

    static func warning() {
        UINotificationFeedbackGenerator().notificationOccurred(.warning)
    }

    static func error() {
        UINotificationFeedbackGenerator().notificationOccurred(.error)
    }

    static func selection() {
        UISelectionFeedbackGenerator().selectionChanged()
    }
}

// MARK: - ViewModifier

struct HapticModifier: ViewModifier {
    let style: HapticStyle

    enum HapticStyle {
        case light, medium, heavy, success, warning, error, selection
    }

    func body(content: Content) -> some View {
        content.simultaneousGesture(
            TapGesture().onEnded {
                switch style {
                case .light: HapticManager.light()
                case .medium: HapticManager.medium()
                case .heavy: HapticManager.heavy()
                case .success: HapticManager.success()
                case .warning: HapticManager.warning()
                case .error: HapticManager.error()
                case .selection: HapticManager.selection()
                }
            }
        )
    }
}

extension View {
    func haptic(_ style: HapticModifier.HapticStyle) -> some View {
        modifier(HapticModifier(style: style))
    }
}
#else
// macOS stubs
enum HapticManager {
    static func light() {}
    static func medium() {}
    static func heavy() {}
    static func success() {}
    static func warning() {}
    static func error() {}
    static func selection() {}
}

extension View {
    func haptic(_ style: Any) -> some View { self }
}
#endif
