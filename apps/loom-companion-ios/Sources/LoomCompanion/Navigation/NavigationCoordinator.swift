import SwiftUI

/// Coordinates cross-tab navigation requests from child views.
///
/// Injected into the SwiftUI environment by ContentView so any descendant
/// can trigger a tab switch without tightly coupling to the tab state.
/// ContentView observes changes to `pendingSessionID` and `pendingAgentID`
/// via `onChange` and performs the actual tab/section switching.
@Observable
final class NavigationCoordinator {
    /// Pending request to navigate to a session by ID.
    var pendingSessionID: String?

    /// Pending request to navigate to an agent by ID.
    var pendingAgentID: String?

    /// Request navigation to the People tab, Sessions segment, with the
    /// given session selected.
    func navigateToSession(id: String) {
        pendingSessionID = id
    }

    /// Request navigation to the People tab, Agents segment, with the
    /// given agent highlighted.
    func navigateToAgent(id: String) {
        pendingAgentID = id
    }

    /// Clear the pending session navigation after it has been consumed.
    func clearPendingSession() {
        pendingSessionID = nil
    }

    /// Clear the pending agent navigation after it has been consumed.
    func clearPendingAgent() {
        pendingAgentID = nil
    }
}

// MARK: - Environment Key

private struct NavigationCoordinatorKey: EnvironmentKey {
    static let defaultValue: NavigationCoordinator? = nil
}

extension EnvironmentValues {
    var navigationCoordinator: NavigationCoordinator? {
        get { self[NavigationCoordinatorKey.self] }
        set { self[NavigationCoordinatorKey.self] = newValue }
    }
}
