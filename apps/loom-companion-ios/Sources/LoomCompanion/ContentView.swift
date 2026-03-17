import SwiftUI
import LoomCompanionKit

struct ContentView: View {
    @Bindable var connectionVM: ConnectionViewModel
    @Binding var pendingDeepLink: DeepLink?
    @State private var healthMonitor = ConnectionHealthMonitor()
    @State private var alertsViewModel = AlertsViewModel()
    @State private var selectedTab: AppTab = .dashboard
    @State private var sseClient: SSEClient?
    @State private var sseBroadcaster = SSEEventBroadcaster()
    @State private var pendingSessionDeepLinkID: String?
    @State private var pendingWorkflowDeepLinkID: String?
    @State private var pendingEndSessionPrefillID: String?

    enum AppTab {
        case dashboard
        case agents
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
        .onChange(of: selectedTab) { _, _ in
            HapticManager.selection()
        }
        .onChange(of: pendingDeepLink) { _, link in
            guard let link else { return }
            handleDeepLink(link)
            pendingDeepLink = nil
        }
    }

    private func handleDeepLink(_ link: DeepLink) {
        switch link {
        case .dashboard:
            selectedTab = .dashboard
        case .session(let id):
            pendingSessionDeepLinkID = id
            selectedTab = .agents
        case .sessions:
            selectedTab = .agents
        case .workflow(let id, _):
            pendingWorkflowDeepLinkID = id
            selectedTab = .ops
        case .tasks:
            selectedTab = .ops
        case .alerts:
            selectedTab = .alerts
        case .connection:
            selectedTab = .connection
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
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel, broadcaster: sseBroadcaster)
            }
            .tabItem { Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent") }
            .tag(AppTab.dashboard)

            AgentsListView(
                apiClient: connectionVM.buildAPIClient(),
                broadcaster: sseBroadcaster,
                deepLinkSessionID: $pendingSessionDeepLinkID,
                onPrefillEndSession: { sessionID in
                    pendingEndSessionPrefillID = sessionID
                    selectedTab = .ops
                }
            )
            .tabItem { Label("Agents", systemImage: "person.2.wave.2") }
            .tag(AppTab.agents)

            NavigationStack {
                OpsView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster,
                    deepLinkWorkflowID: $pendingWorkflowDeepLinkID,
                    prefillEndSessionID: $pendingEndSessionPrefillID
                )
            }
            .tabItem { Label("Ops", systemImage: "square.grid.2x2") }
            .tag(AppTab.ops)

            NavigationStack {
                AlertsListView(viewModel: alertsViewModel) { action, alert in
                    switch action {
                    case .viewSession:
                        pendingSessionDeepLinkID = alert.relatedSessionId
                        selectedTab = .agents
                    case .viewWorkflow:
                        pendingWorkflowDeepLinkID = alert.relatedWorkflowId
                        selectedTab = .ops
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
                Label("Agents", systemImage: "person.2.wave.2")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .agents }
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
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel, broadcaster: sseBroadcaster)
            case .agents:
                AgentsListView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster,
                    deepLinkSessionID: $pendingSessionDeepLinkID,
                    onPrefillEndSession: { sessionID in
                        pendingEndSessionPrefillID = sessionID
                        selectedTab = .ops
                    }
                )
            case .ops:
                OpsView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster,
                    deepLinkWorkflowID: $pendingWorkflowDeepLinkID,
                    prefillEndSessionID: $pendingEndSessionPrefillID
                )
            case .alerts:
                AlertsListView(viewModel: alertsViewModel) { action, alert in
                    switch action {
                    case .viewSession:
                        pendingSessionDeepLinkID = alert.relatedSessionId
                        selectedTab = .agents
                    case .viewWorkflow:
                        pendingWorkflowDeepLinkID = alert.relatedWorkflowId
                        selectedTab = .ops
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
        let client = SSEClient(request: request, session: apiClient.sseSession())
        client.onStateChange = { [weak healthMonitor] state in
            healthMonitor?.handleSSEStateChange(state)
        }
        sseClient = client
        client.connect()
        sseBroadcaster.start(sseClient: client)
    }

    private func teardownSSE() {
        sseBroadcaster.stop()
        sseClient?.disconnect()
        sseClient = nil
    }
}
