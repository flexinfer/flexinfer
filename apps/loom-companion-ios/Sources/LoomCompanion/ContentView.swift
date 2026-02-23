import SwiftUI
import LoomCompanionKit

struct ContentView: View {
    @Bindable var connectionVM: ConnectionViewModel
    @State private var healthMonitor = ConnectionHealthMonitor()
    @State private var selectedTab: AppTab = .dashboard

    enum AppTab {
        case dashboard
        case sessions
        case connection
    }

    var body: some View {
        if connectionVM.isAuthenticated {
            authenticatedContent
        } else {
            LoginView(viewModel: connectionVM)
        }
    }

    @ViewBuilder
    private var authenticatedContent: some View {
        #if os(iOS)
        if UIDevice.current.userInterfaceIdiom == .pad {
            iPadLayout
        } else {
            iPhoneLayout
        }
        #else
        iPhoneLayout
        #endif
    }

    private var iPhoneLayout: some View {
        TabView(selection: $selectedTab) {
            NavigationStack {
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor)
            }
            .tabItem { Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent") }
            .tag(AppTab.dashboard)

            NavigationStack {
                SessionsListView(apiClient: connectionVM.buildAPIClient())
            }
            .tabItem { Label("Sessions", systemImage: "list.bullet.rectangle") }
            .tag(AppTab.sessions)

            NavigationStack {
                ConnectionDiagnosticsView(
                    connectionVM: connectionVM,
                    healthMonitor: healthMonitor
                )
            }
            .tabItem { Label("Connection", systemImage: "network") }
            .tag(AppTab.connection)
        }
    }

    private var iPadLayout: some View {
        NavigationSplitView {
            List(selection: $selectedTab) {
                Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent")
                    .tag(AppTab.dashboard)
                Label("Sessions", systemImage: "list.bullet.rectangle")
                    .tag(AppTab.sessions)
                Label("Connection", systemImage: "network")
                    .tag(AppTab.connection)
            }
            .navigationTitle("Loom")
        } detail: {
            switch selectedTab {
            case .dashboard:
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor)
            case .sessions:
                SessionsListView(apiClient: connectionVM.buildAPIClient())
            case .connection:
                ConnectionDiagnosticsView(
                    connectionVM: connectionVM,
                    healthMonitor: healthMonitor
                )
            }
        }
    }
}
