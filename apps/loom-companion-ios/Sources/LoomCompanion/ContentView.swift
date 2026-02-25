import SwiftUI
import LoomCompanionKit

struct ContentView: View {
    @Bindable var connectionVM: ConnectionViewModel
    @State private var healthMonitor = ConnectionHealthMonitor()
    @State private var alertsViewModel = AlertsViewModel()
    @State private var selectedTab: AppTab = .dashboard
    @State private var sseClient: SSEClient?

    enum AppTab {
        case dashboard
        case sessions
        case ops
        case alerts
        case connection
    }

    var body: some View {
        Group {
            if connectionVM.isAuthenticated {
                authenticatedContent
            } else {
                LoginView(viewModel: connectionVM)
            }
        }
        .task {
            if connectionVM.isAuthenticated {
                setupSSE()
            }
        }
        .onChange(of: connectionVM.isAuthenticated) { _, isAuth in
            if isAuth {
                setupSSE()
            } else {
                teardownSSE()
            }
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
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel, sseClient: sseClient)
            }
            .tabItem { Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent") }
            .tag(AppTab.dashboard)

            NavigationStack {
                SessionsListView(apiClient: connectionVM.buildAPIClient())
            }
            .tabItem { Label("Sessions", systemImage: "list.bullet.rectangle") }
            .tag(AppTab.sessions)

            NavigationStack {
                OpsView(apiClient: connectionVM.buildAPIClient())
            }
            .tabItem { Label("Ops", systemImage: "square.grid.2x2") }
            .tag(AppTab.ops)

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
            List {
                Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .dashboard }
                Label("Sessions", systemImage: "list.bullet.rectangle")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .sessions }
                Label("Ops", systemImage: "square.grid.2x2")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .ops }
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
                .contentShape(Rectangle())
                .onTapGesture { selectedTab = .alerts }
                Label("Connection", systemImage: "network")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .connection }
            }
            .navigationTitle("Loom")
        } detail: {
            switch selectedTab {
            case .dashboard:
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel, sseClient: sseClient)
            case .sessions:
                SessionsListView(apiClient: connectionVM.buildAPIClient())
            case .ops:
                OpsView(apiClient: connectionVM.buildAPIClient())
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

    // MARK: - SSE Lifecycle

    private func setupSSE() {
        guard sseClient == nil else { return }
        guard let apiClient = connectionVM.buildAPIClient(),
              let request = try? apiClient.sseRequest()
        else { return }
        let client = SSEClient(request: request)
        client.onStateChange = { [weak healthMonitor] state in
            healthMonitor?.handleSSEStateChange(state)
        }
        sseClient = client
        client.connect()
    }

    private func teardownSSE() {
        sseClient?.disconnect()
        sseClient = nil
    }
}
