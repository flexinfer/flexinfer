import SwiftUI

enum LoomTypography {
    // MARK: - Display (Dashboard hero numbers)

    static let displayLarge = Font.system(size: 34, weight: .bold, design: .rounded)
    static let displayMedium = Font.system(size: 28, weight: .bold, design: .rounded)

    // MARK: - Headings

    static let headlineLarge = Font.system(size: 20, weight: .semibold)
    static let headlineMedium = Font.system(size: 17, weight: .semibold)

    // MARK: - Body

    static let bodyRegular = Font.system(size: 15, weight: .regular)
    static let bodyMedium = Font.system(size: 15, weight: .medium)

    // MARK: - Labels & Captions

    static let labelLarge = Font.system(size: 13, weight: .medium)
    static let labelSmall = Font.system(size: 11, weight: .medium)
    static let caption = Font.system(size: 12, weight: .regular)

    // MARK: - Mono (IDs, event types)

    static let monoSmall = Font.system(size: 11, weight: .regular, design: .monospaced)
    static let monoCaption = Font.system(size: 10, weight: .regular, design: .monospaced)

    // MARK: - Counter (animated numbers on dashboard)

    static let counterLarge = Font.system(size: 32, weight: .bold, design: .rounded)
    static let counterMedium = Font.system(size: 22, weight: .semibold, design: .rounded)
    static let counterSmall = Font.system(size: 17, weight: .semibold, design: .rounded)
}
