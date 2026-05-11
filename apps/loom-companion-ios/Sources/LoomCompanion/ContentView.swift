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
    @State private var selectedPeopleSection: PeopleSection = .agents
    @State private var navigationCoordinator = NavigationCoordinator()
    @State private var pendingAgentDeepLinkID: String?

    enum AppTab {
        case dashboard
        case people
        case spawn
        case work
        case mills
        case connection
    }

    enum PeopleSection: String, CaseIterable, Identifiable {
        case agents
        case sessions
        case live

        var id: String { rawValue }
    }

    var body: some View {
        Group {
            if connectionVM.isAuthenticated {
                authenticatedContent
            } else {
                LoginView(viewModel: connectionVM)
            }
        }
        .environment(\.navigationCoordinator, navigationCoordinator)
        .task {
            // Launch-argument deep links are seeded in App.init and land here
            // as the INITIAL value of `pendingDeepLink`. `.onChange` doesn't
            // fire for initial values, so consume it explicitly on first
            // appear before any other connection-setup work runs.
            if let link = pendingDeepLink {
                handleDeepLink(link)
                pendingDeepLink = nil
            }
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
        .tint(LoomColors.info)
        .preferredColorScheme(.dark)
        .onChange(of: selectedTab) { _, _ in
            HapticManager.selection()
        }
        .onChange(of: selectedPeopleSection) { _, _ in
            HapticManager.selection()
        }
        .onChange(of: pendingDeepLink) { _, link in
            guard let link else { return }
            handleDeepLink(link)
            pendingDeepLink = nil
        }
        .onChange(of: navigationCoordinator.pendingSessionID) { _, sessionId in
            guard let sessionId else { return }
            pendingSessionDeepLinkID = sessionId
            selectedPeopleSection = .sessions
            selectedTab = .people
            navigationCoordinator.clearPendingSession()
        }
        .onChange(of: navigationCoordinator.pendingAgentID) { _, agentId in
            guard let agentId else { return }
            pendingAgentDeepLinkID = agentId
            selectedPeopleSection = .agents
            selectedTab = .people
            navigationCoordinator.clearPendingAgent()
        }
        .onChange(of: navigationCoordinator.pendingSpawnID) { _, spawnId in
            guard spawnId != nil else { return }
            selectedTab = .spawn
            navigationCoordinator.clearPendingSpawn()
        }
        .onChange(of: navigationCoordinator.pendingWorkflowID) { _, workflowId in
            guard let workflowId else { return }
            pendingWorkflowDeepLinkID = workflowId
            selectedTab = .work
            navigationCoordinator.clearPendingWorkflow()
        }
        .onChange(of: navigationCoordinator.pendingTasksFilter) { _, filter in
            guard filter != nil else { return }
            selectedTab = .work
        }
        .onChange(of: navigationCoordinator.pendingHandoffInbox) { _, open in
            guard open else { return }
            selectedTab = .work
        }
    }

    private func handleDeepLink(_ link: DeepLink) {
        // Dispatch based on the specific link; destinationGroup handles the tab.
        switch link {
        case .dashboard:
            selectedTab = .dashboard

        case .people:
            selectedTab = .people

        case .work:
            selectedTab = .work

        case .alerts:
            // Alerts tab removed in Spawn-tab promotion. Route to Dashboard,
            // which still surfaces the unread-alerts count and the critical
            // "DO NEXT" card sourced from AlertsViewModel.
            selectedTab = .dashboard

        case .connection:
            selectedTab = .connection

        // People · single session / agent
        case .session(let id):
            pendingSessionDeepLinkID = id
            selectedPeopleSection = .sessions
            selectedTab = .people

        case .agent(let id):
            pendingAgentDeepLinkID = id
            selectedPeopleSection = .agents
            selectedTab = .people

        // People · filtered lists (preset filter + navigate)
        case .sessions(let status, let agentId):
            navigationCoordinator.filterSessions(status: status, agentId: agentId)
            selectedPeopleSection = .sessions
            selectedTab = .people

        case .agents(let status, let type):
            navigationCoordinator.filterAgents(status: status, type: type)
            selectedPeopleSection = .agents
            selectedTab = .people

        // Work · tasks filter
        case .tasks(let status, let agentId, let sessionId):
            navigationCoordinator.filterTasks(status: status, agentId: agentId, sessionId: sessionId)
            selectedTab = .work

        // Work · workflow (with optional approve intent)
        case .workflow(let id, let approve):
            if approve {
                Task { await approveWorkflowFromDeepLink(workflowId: id) }
            }
            pendingWorkflowDeepLinkID = id
            selectedTab = .work

        // Spawn · active remote execution
        case .spawn(let id):
            navigationCoordinator.navigateToSpawn(id: id)
            selectedTab = .spawn

        // Work · handoff inbox
        case .handoff:
            navigationCoordinator.openHandoffInbox()
            selectedTab = .work

        // Alerts · single alert — Alerts tab is gone in the Spawn-tab
        // promotion. Hand the alert id to the navigation coordinator (which
        // will surface it on the Dashboard once an alerts sheet/detail lands)
        // and route to Dashboard.
        case .alert(let id):
            navigationCoordinator.navigateToAlert(id: id)
            selectedTab = .dashboard

        // One-shot bootstrap from `make mobile-app-run-device` over USB.
        case .configure(let spec):
            Task { await connectionVM.applyConfigureSpec(spec) }
            selectedTab = .connection
        }
    }

    private func approveWorkflowFromDeepLink(workflowId: String) async {
        guard let apiClient = connectionVM.buildAPIClient() else { return }
        // Fetch workflow detail to find the pending step.
        do {
            let detail: MobileWorkflowDetail = try await apiClient.request(.workflowDetail(id: workflowId))
            guard let pendingStep = detail.steps?.first(where: { $0.status == .waitingApproval }) else { return }
            // Approve returns the workflow detail; discard it.
            let _: MobileWorkflowDetail = try await apiClient.request(
                .workflowApprove(id: workflowId, stepId: pendingStep.id)
            )
        } catch {
            // Best-effort - user can manually approve in the work view.
            print("[DeepLink] Workflow approval failed: \(error)")
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
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel, broadcaster: sseBroadcaster) { action in
                    switch action {
                    case .people:
                        selectedPeopleSection = .agents
                        selectedTab = .people
                    case .work:
                        selectedTab = .work
                    case .connection:
                        selectedTab = .connection
                    case .liveActivities:
                        selectedPeopleSection = .sessions
                        selectedTab = .people
                    case .alerts:
                        // Alerts tab replaced by Spawn. Unread-alert cards
                        // surface on the Dashboard itself.
                        selectedTab = .dashboard
                    }
                }
            }
            .tabItem { Label("Dashboard", systemImage: "gauge.open.with.lines.needle.33percent") }
            .tag(AppTab.dashboard)

            peopleTab
                .tabItem { Label("Agents", systemImage: "person.2.wave.2") }
                .tag(AppTab.people)

            NavigationStack {
                SpawnTabView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster
                )
            }
            .tabItem { Label("Spawn", systemImage: "sparkles") }
            .tag(AppTab.spawn)

            NavigationStack {
                OpsView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster,
                    deepLinkWorkflowID: $pendingWorkflowDeepLinkID,
                    taskFilter: Binding(
                        get: { navigationCoordinator.pendingTasksFilter },
                        set: { navigationCoordinator.pendingTasksFilter = $0 }
                    ),
                    prefillEndSessionID: $pendingEndSessionPrefillID
                )
            }
            .tabItem { Label("Work", systemImage: "square.grid.2x2") }
            .tag(AppTab.work)

            NavigationStack {
                MillsScreen(apiClient: connectionVM.buildAPIClient())
            }
            .tabItem { Label("Mills", systemImage: "hexagon") }
            .tag(AppTab.mills)

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
                    .onTapGesture { selectedTab = .people }
                Label("Spawn", systemImage: "sparkles")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .spawn }
                Label("Work", systemImage: "square.grid.2x2")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .work }
                Label("Mills", systemImage: "hexagon")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .mills }
                Label("Connection", systemImage: "network")
                    .contentShape(Rectangle())
                    .onTapGesture { selectedTab = .connection }
            }
            .navigationTitle("Loom")
        } detail: {
            switch selectedTab {
            case .dashboard:
                DashboardView(apiClient: connectionVM.buildAPIClient(), healthMonitor: healthMonitor, alertsViewModel: alertsViewModel, broadcaster: sseBroadcaster) { action in
                    switch action {
                    case .people:
                        selectedPeopleSection = .agents
                        selectedTab = .people
                    case .work:
                        selectedTab = .work
                    case .connection:
                        selectedTab = .connection
                    case .liveActivities:
                        selectedPeopleSection = .sessions
                        selectedTab = .people
                    case .alerts:
                        selectedTab = .dashboard
                    }
                }
            case .people:
                peopleTab
            case .spawn:
                SpawnTabView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster
                )
            case .work:
                OpsView(
                    apiClient: connectionVM.buildAPIClient(),
                    broadcaster: sseBroadcaster,
                    deepLinkWorkflowID: $pendingWorkflowDeepLinkID,
                    taskFilter: Binding(
                        get: { navigationCoordinator.pendingTasksFilter },
                        set: { navigationCoordinator.pendingTasksFilter = $0 }
                    ),
                    prefillEndSessionID: $pendingEndSessionPrefillID
                )
            case .mills:
                MillsScreen(apiClient: connectionVM.buildAPIClient())
            case .connection:
                ConnectionDiagnosticsView(
                    connectionVM: connectionVM,
                    healthMonitor: healthMonitor
                )
            }
        }
    }

    @ViewBuilder
    private var peopleTab: some View {
        VStack(spacing: LoomSpacing.sm) {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                HStack(alignment: .center, spacing: LoomSpacing.sm) {
                    Text("Agents")
                        .font(.largeTitle.bold())
                        .foregroundStyle(LoomColors.textPrimary)
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                        .layoutPriority(1)

                    // Spawn shortcut — Agents tab is where you see the
                    // current fleet, so make "create a new one" one tap away.
                    Button {
                        selectedTab = .spawn
                    } label: {
                        Image(systemName: "sparkles")
                            .font(.title3)
                            .foregroundStyle(LoomColors.accent)
                            .padding(.horizontal, 4)
                    }
                    .accessibilityLabel("Spawn new agent")

                    Spacer(minLength: LoomSpacing.sm)
                }

                Picker("Agents Section", selection: $selectedPeopleSection) {
                    Text("Roster").tag(PeopleSection.agents)
                    Text("Sessions").tag(PeopleSection.sessions)
                    Text("Live").tag(PeopleSection.live)
                }
                .pickerStyle(.segmented)

                Text(peopleSectionSubtitle)
                    .font(.caption)
                    .foregroundStyle(LoomColors.textSecondary)
                    .lineLimit(2)
            }
            .padding(.horizontal)
            .padding(.top, LoomSpacing.sm)
            .padding(.bottom, LoomSpacing.xs)

            Group {
                switch selectedPeopleSection {
                case .agents:
                    AgentsListView(
                        apiClient: connectionVM.buildAPIClient(),
                        broadcaster: sseBroadcaster,
                        deepLinkSessionID: $pendingSessionDeepLinkID,
                        embeddedInPeopleTab: true,
                        onPrefillEndSession: { sessionID in
                            pendingEndSessionPrefillID = sessionID
                            selectedTab = .work
                        }
                    )
                case .sessions:
                    SessionsListView(
                        apiClient: connectionVM.buildAPIClient(),
                        deepLinkSessionID: $pendingSessionDeepLinkID,
                        embeddedInPeopleTab: true,
                        onPrefillEndSession: { sessionID in
                            pendingEndSessionPrefillID = sessionID
                            selectedTab = .work
                        }
                    )
                case .live:
                    LiveSessionsView(broadcaster: sseBroadcaster)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }

    private var peopleSectionSubtitle: String {
        switch selectedPeopleSection {
        case .agents:
            return "Every agent on your fleet — live, idle, offline."
        case .sessions:
            return "Agent sessions across every namespace."
        case .live:
            return "Live tool calls flowing through every active session — public-tier redacted."
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
