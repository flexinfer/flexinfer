import SwiftUI
import LoomCompanionKit

struct ContentView: View {
    @Bindable var connectionVM: ConnectionViewModel
    @State private var healthMonitor = ConnectionHealthMonitor()
    @State private var alertsViewModel = AlertsViewModel()
    @State private var selectedTab: AppTab = .dashboard

    enum AppTab {
        case dashboard
        case sessions
        case alerts
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
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel)
            }
            .tabItem { Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent") }
            .tag(AppTab.dashboard)

            NavigationStack {
                SessionsListView(apiClient: connectionVM.buildAPIClient())
            }
            .tabItem { Label("Sessions", systemImage: "list.bullet.rectangle") }
            .tag(AppTab.sessions)

            NavigationStack {
                AlertsListView(viewModel: alertsViewModel) { action, _ in
                    switch action {
                    case .viewSession:
                        selectedTab = .sessions
                    case .viewDashboard:
                        selectedTab = .dashboard
                    case .acknowledge:
                        break
                    }
                }
            }
            .tabItem { Label("Alerts", systemImage: "bell") }
            .tag(AppTab.alerts)
            .badge(alertsViewModel.unreadCount)

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
                Label {
                    Text("Alerts")
                } icon: {
                    Image(systemName: "bell")
                        .overlay(alignment: .topTrailing) {
                            if alertsViewModel.unreadCount > 0 {
                                Text("\(min(alertsViewModel.unreadCount, 99))")
                                    .font(.caption2)
                                    .fontWeight(.bold)
                                    .foregroundStyle(.white)
                                    .padding(3)
                                    .background(.red, in: Circle())
                                    .offset(x: 8, y: -8)
                            }
                        }
                }
                .tag(AppTab.alerts)
                Label("Connection", systemImage: "network")
                    .tag(AppTab.connection)
            }
            .navigationTitle("Loom")
        } detail: {
            switch selectedTab {
            case .dashboard:
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel)
            case .sessions:
                SessionsListView(apiClient: connectionVM.buildAPIClient())
            case .alerts:
                AlertsListView(viewModel: alertsViewModel) { action, _ in
                    switch action {
                    case .viewSession:
                        selectedTab = .sessions
                    case .viewDashboard:
                        selectedTab = .dashboard
                    case .acknowledge:
                        break
                    }
                }
            case .connection:
                ConnectionDiagnosticsView(
                    connectionVM: connectionVM,
                    healthMonitor: healthMonitor
                )
            }
        }
    }
}
