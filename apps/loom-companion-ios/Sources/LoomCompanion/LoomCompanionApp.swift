import SwiftUI
import LoomCompanionKit

@main
struct LoomCompanionApp: App {
    @State private var connectionVM = ConnectionViewModel()

    var body: some Scene {
        WindowGroup {
            ContentView(connectionVM: connectionVM)
        }
    }
}
