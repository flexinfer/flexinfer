import SwiftUI
import LoomCompanionKit

#if canImport(UIKit)
import UIKit
#endif

/// Share-link affordance for any `DeepLink`. Wraps SwiftUI's `ShareLink` with
/// Loom-specific defaults (title + preview image) and a matching Button-style
/// "Copy link" variant that writes the URL to the pasteboard with haptic feedback.
///
/// Usage in a detail-view toolbar:
/// ```swift
/// .toolbar {
///     ToolbarItem(placement: .primaryAction) {
///         LoomShareLink(link: .session(id: sessionId))
///     }
/// }
/// ```
struct LoomShareLink: View {
    let link: DeepLink
    var label: String?

    init(link: DeepLink, label: String? = nil) {
        self.link = link
        self.label = label
    }

    var body: some View {
        if let url = link.url {
            ShareLink(item: url, subject: Text(link.shareTitle), message: Text(link.shareTitle)) {
                Label(label ?? "Share", systemImage: "square.and.arrow.up")
            }
        } else {
            EmptyView()
        }
    }
}

/// Menu button that copies the canonical `loom://` URL for a deep link to the
/// pasteboard. Gives haptic feedback on success. Use inside `.contextMenu {}`
/// or `Menu {}` blocks so the user can long-press a row to grab a shareable
/// link without leaving the surface.
struct LoomCopyLinkButton: View {
    let link: DeepLink
    var label: String = "Copy link"

    var body: some View {
        Button {
            copy()
        } label: {
            Label(label, systemImage: "link")
        }
    }

    private func copy() {
        guard let url = link.url else { return }
        #if canImport(UIKit)
        UIPasteboard.general.url = url
        UIPasteboard.general.string = url.absoluteString
        #endif
        HapticManager.light()
    }
}

// MARK: - Convenience row context menu

extension View {
    /// Attach a context menu with a "Copy link" entry for any deep link.
    /// Safe to nest: if additional menu items are needed, use a full `.contextMenu {}`
    /// block manually and add `LoomCopyLinkButton(link:)` alongside.
    func loomShareContextMenu(_ link: DeepLink) -> some View {
        contextMenu {
            LoomCopyLinkButton(link: link)
            if let url = link.url {
                ShareLink(item: url, subject: Text(link.shareTitle)) {
                    Label("Share", systemImage: "square.and.arrow.up")
                }
            }
        }
    }
}

#Preview("LoomShareLink") {
    VStack(alignment: .leading, spacing: 16) {
        Text("Share primitives")
            .font(LoomTypography.headlineMedium)
            .foregroundStyle(LoomColors.fgPrimary)

        Menu {
            LoomCopyLinkButton(link: .session(id: "svc-abc123"))
            LoomShareLink(link: .session(id: "svc-abc123"))
        } label: {
            Label("Session menu", systemImage: "ellipsis.circle")
                .foregroundStyle(LoomColors.info)
        }

        LoomShareLink(link: .agents(status: "active", type: "claude-code"))
            .foregroundStyle(LoomColors.info)

        LoomShareLink(link: .tasks(status: "blocked", agentId: nil, sessionId: nil))
            .foregroundStyle(LoomColors.info)
    }
    .padding()
    .background(LoomColors.bgPrimary)
    .preferredColorScheme(.dark)
}
