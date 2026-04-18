import SwiftUI
import LoomCompanionKit

@main
struct LoomCompanionApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @State private var connectionVM = ConnectionViewModel()
    @State private var pendingDeepLink: DeepLink?

    init() {
        // Launch-argument deep link. `make mobile-app-run-device` (and the
        // simulator equivalent) pass a `loom://configure?...` URL as the
        // first non-flag launch argument so a freshly installed dev build
        // lands already connected to the cluster HUD without the user
        // retyping secrets. The URL rides over USB via `devicectl process
        // launch` (or `simctl launch`), lives only in-process, and is
        // consumed by ContentView.handleDeepLink on first render.
        if let link = Self.deepLinkFromLaunchArgs() {
            _pendingDeepLink = State(initialValue: link)
        }
    }

    var body: some Scene {
        WindowGroup {
            ContentView(connectionVM: connectionVM, pendingDeepLink: $pendingDeepLink)
                .onOpenURL { url in
                    guard let link = DeepLink.from(url) else { return }
                    // `.configure` carries bearer + CF service secrets in the
                    // URL. Only accept it from launch arguments (USB-trusted
                    // channel from `make mobile-app-run-device`), never from
                    // `onOpenURL` where it could arrive via Mail/Messages and
                    // hijack the session. Silently drop.
                    if case .configure = link { return }
                    pendingDeepLink = link
                }
        }
    }

    /// Scan the process launch args for a `loom://` URL and parse it.
    /// Accepts both `loom://…` as a bare positional arg and the form
    /// `--configure-url=loom://…` for scripts that want a named flag.
    private static func deepLinkFromLaunchArgs() -> DeepLink? {
        for raw in ProcessInfo.processInfo.arguments.dropFirst() {
            let candidate: String
            if raw.hasPrefix("--configure-url=") {
                candidate = String(raw.dropFirst("--configure-url=".count))
            } else if raw.hasPrefix("loom://") {
                candidate = raw
            } else {
                continue
            }
            if let url = URL(string: candidate), let link = DeepLink.from(url) {
                return link
            }
        }
        return nil
    }
}
