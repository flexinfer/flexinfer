import SwiftUI
import LoomCompanionKit

@main
struct LoomCompanionApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @State private var connectionVM = ConnectionViewModel()
    @State private var pendingDeepLink: DeepLink?

    var body: some Scene {
        WindowGroup {
            ContentView(connectionVM: connectionVM, pendingDeepLink: $pendingDeepLink)
                .onOpenURL { url in
                    pendingDeepLink = DeepLink.from(url)
                }
        }
    }
}
