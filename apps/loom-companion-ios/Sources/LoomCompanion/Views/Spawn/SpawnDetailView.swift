import SwiftUI
import LoomCompanionKit

/// Detail view for a single headless agent spawn. Shows a status header, a
/// telemetry summary (turns, cost, tokens) and four tabs: Tools, Files,
/// Errors, Usage. Reachable from a `NavigationLink` on `SpawnAgentView`.
///
/// Telemetry comes from `GET /api/mobile/v1/agent/spawn/{id}/telemetry` and
/// the paginated sub-endpoints. Wave 2 only loads the first page for each
/// tab; incremental pagination and SSE-driven updates are deferred to
/// slices 14d/14e and the backend delta slice (W2-3).
struct SpawnDetailView: View {
    let spawn: MobileSpawnStatus
    let viewModel: SpawnViewModel
    let apiClient: any LoomAPIClientProtocol

    @State private var telemetry: SpawnTelemetry?
    @State private var toolsPage: SpawnTelemetryToolsPage?
    @State private var filesPage: SpawnTelemetryFilesPage?
    @State private var errorsPage: SpawnTelemetryErrorsPage?

    @State private var isLoadingTelemetry = false
    @State private var isLoadingTools = false
    @State private var isLoadingFiles = false
    @State private var isLoadingErrors = false
    @State private var loadError: String?

    @State private var selectedTab: DetailTab = .tools
    @State private var showingStopConfirmation = false
    @State private var showingInterruptConfirmation = false
    @State private var followUpText = ""
    @State private var isSending = false
    @State private var isInterrupting = false
    @State private var sendStatus: SendStatus = .idle
    @State private var alertMessage: AlertMessage?

    /// One-shot toast/HUD-style status for the message-send affordance.
    private enum SendStatus: Equatable {
        case idle
        case sending
        case sent

        var label: String? {
            switch self {
            case .idle: return nil
            case .sending: return "Sending\u{2026}"
            case .sent: return "Sent"
            }
        }
    }

    private struct AlertMessage: Identifiable {
        let id = UUID()
        let title: String
        let body: String
    }

    /// True only when the spawn is in a state that accepts follow-up messages
    /// or interrupts. Multi-turn opt-in is gated on `request.multiTurn` so we
    /// don't show the input row for one-shot spawns where the server rejects
    /// it with 409 anyway.
    private var canSendFollowUp: Bool {
        spawn.request.multiTurn == true && spawn.status == "running"
    }

    private enum DetailTab: String, Hashable, CaseIterable {
        case tools, files, errors, usage, activity

        var title: String {
            switch self {
            case .tools: return "Tools"
            case .files: return "Files"
            case .errors: return "Errors"
            case .usage: return "Usage"
            case .activity: return "Activity"
            }
        }

        var systemImage: String {
            switch self {
            case .tools: return "wrench.and.screwdriver"
            case .files: return "doc.text"
            case .errors: return "exclamationmark.triangle"
            case .usage: return "chart.bar.xaxis"
            case .activity: return "bolt.horizontal"
            }
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    statusHeader
                    telemetrySummaryCard
                    tabPicker
                    tabContent
                    if let loadError {
                        Text(loadError)
                            .font(.caption)
                            .foregroundStyle(LoomColors.statusCritical)
                            .padding(.horizontal)
                    }
                }
                .padding(.vertical)
            }
            if spawn.request.multiTurn == true {
                followUpInputRow
            }
        }
        .navigationTitle(spawn.request.project)
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    LoomCopyLinkButton(link: .spawn(id: spawn.spawnId))
                    LoomShareLink(link: .spawn(id: spawn.spawnId))
                } label: {
                    Label("Share", systemImage: "square.and.arrow.up")
                }
            }
            ToolbarItemGroup(placement: .primaryAction) {
                if spawn.isActive {
                    if spawn.request.multiTurn == true {
                        Button("Interrupt", role: .destructive) {
                            showingInterruptConfirmation = true
                        }
                        .disabled(isInterrupting)
                        .accessibilityIdentifier("spawn.interrupt.button")
                    }
                    Button("Stop", role: .destructive) {
                        showingStopConfirmation = true
                    }
                }
            }
        }
        .confirmationDialog(
            "Stop Spawn?",
            isPresented: $showingStopConfirmation,
            titleVisibility: .visible
        ) {
            Button("Stop Agent", role: .destructive) {
                Task { await viewModel.stopSpawn(id: spawn.spawnId) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This will terminate the running agent and clean up its pod.")
        }
        .confirmationDialog(
            "Interrupt this turn?",
            isPresented: $showingInterruptConfirmation,
            titleVisibility: .visible
        ) {
            Button("Interrupt", role: .destructive) {
                Task { await performInterrupt() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Aborts the in-flight turn. The agent stays running and you can send a new message after.")
        }
        .alert(item: $alertMessage) { msg in
            Alert(
                title: Text(msg.title),
                message: Text(msg.body),
                dismissButton: .default(Text("OK"))
            )
        }
        .task {
            await loadAll()
        }
        .refreshable {
            await loadAll()
        }
    }

    // MARK: - Follow-up message input

    /// Bottom-pinned row for sending follow-up messages to a multi-turn spawn.
    /// Disabled when the spawn is not in a state that accepts new turns
    /// (matches the server's 409 semantics — only `running` is accepted).
    private var followUpInputRow: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let label = sendStatus.label {
                Text(label)
                    .font(.caption2)
                    .foregroundStyle(sendStatus == .sent ? LoomColors.statusHealthy : LoomColors.fgSecondary)
                    .accessibilityIdentifier("spawn.message.status")
            }
            HStack(alignment: .bottom, spacing: 8) {
                TextField(
                    "Send a follow-up message\u{2026}",
                    text: $followUpText,
                    axis: .vertical
                )
                .lineLimit(1 ... 5)
                .textFieldStyle(.roundedBorder)
                .disabled(!canSendFollowUp || isSending)
                .accessibilityIdentifier("spawn.message.field")

                Button(action: { Task { await performSend() } }) {
                    if isSending {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Image(systemName: "paperplane.fill")
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!canSendFollowUp || isSending || trimmedFollowUp.isEmpty)
                .accessibilityLabel("Send")
                .accessibilityIdentifier("spawn.message.send")
            }
            if !canSendFollowUp {
                Text(disabledHint)
                    .font(.caption2)
                    .foregroundStyle(LoomColors.fgMuted)
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 10)
        .background(LoomColors.bgSecondary)
        .overlay(alignment: .top) {
            Divider().background(LoomColors.border)
        }
    }

    private var trimmedFollowUp: String {
        followUpText.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private var disabledHint: String {
        switch spawn.status {
        case "running": return ""
        case "creating": return "Spawn is still starting up\u{2026}"
        case "completed", "stopped": return "Spawn has finished — start a new one to continue."
        case "failed": return "Spawn failed — start a new one to continue."
        default: return "Spawn is not in a state that accepts messages."
        }
    }

    private func performSend() async {
        let text = trimmedFollowUp
        guard !text.isEmpty, canSendFollowUp else { return }
        isSending = true
        sendStatus = .sending
        defer { isSending = false }
        do {
            _ = try await viewModel.sendMessage(spawnId: spawn.spawnId, message: text)
            followUpText = ""
            sendStatus = .sent
            // Auto-clear the "Sent" hint after a short beat so the row goes
            // back to a clean state without the user having to dismiss it.
            Task { @MainActor in
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                if sendStatus == .sent { sendStatus = .idle }
            }
        } catch let err as LoomAPIError {
            sendStatus = .idle
            alertMessage = AlertMessage(title: "Send Failed", body: err.description)
        } catch {
            sendStatus = .idle
            alertMessage = AlertMessage(title: "Send Failed", body: error.localizedDescription)
        }
    }

    private func performInterrupt() async {
        isInterrupting = true
        defer { isInterrupting = false }
        do {
            _ = try await viewModel.interruptSpawn(id: spawn.spawnId)
        } catch let err as LoomAPIError {
            alertMessage = AlertMessage(title: "Interrupt Failed", body: err.description)
        } catch {
            alertMessage = AlertMessage(title: "Interrupt Failed", body: error.localizedDescription)
        }
    }

    // MARK: - Header

    private var statusHeader: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Label(spawn.request.agentType, systemImage: LoomColors.agentTypeIcon(spawn.request.agentType))
                    .font(.subheadline)
                    .foregroundStyle(LoomColors.agentTypeColor(spawn.request.agentType))
                Spacer()
                StatusBadge(spawn.status, color: spawnDetailStatusColor(spawn.status))
            }

            Text(spawn.request.taskDescription)
                .font(.body)
                .foregroundStyle(LoomColors.fgPrimary)

            HStack(spacing: 16) {
                if let started = formatTime(spawn.startedAt) {
                    Label(started, systemImage: "play.circle")
                        .font(.caption)
                        .foregroundStyle(LoomColors.fgSecondary)
                }
                if let endedAt = spawn.endedAt, let ended = formatTime(endedAt) {
                    Label(ended, systemImage: "stop.circle")
                        .font(.caption)
                        .foregroundStyle(LoomColors.fgSecondary)
                }
            }

            Text(spawn.agentId)
                .font(.caption2)
                .foregroundStyle(LoomColors.fgMuted)
                .monospaced()
        }
        .padding()
        .background(LoomColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    // MARK: - Telemetry Summary

    private var telemetrySummaryCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label("Telemetry", systemImage: "sparkles")
                    .font(.headline)
                    .foregroundStyle(LoomColors.fgPrimary)
                Spacer()
                if isLoadingTelemetry {
                    ProgressView().scaleEffect(0.7)
                }
            }

            if let telemetry {
                HStack(spacing: 24) {
                    metricColumn(
                        label: "Turns",
                        value: "\(telemetry.turnCount)"
                    )
                    metricColumn(
                        label: "Cost",
                        value: formatCost(telemetry.totalCostUSD, estimated: telemetry.costEstimated)
                    )
                    metricColumn(
                        label: "Input",
                        value: formatTokenCount(telemetry.tokenUsage.inputTokens)
                    )
                    metricColumn(
                        label: "Output",
                        value: formatTokenCount(telemetry.tokenUsage.outputTokens)
                    )
                }

                if let stop = telemetry.stopReason, !stop.isEmpty {
                    Label("Stop: \(stop)", systemImage: "flag.checkered")
                        .font(.caption)
                        .foregroundStyle(LoomColors.fgSecondary)
                }
            } else if !isLoadingTelemetry {
                Text("No telemetry yet")
                    .font(.caption)
                    .foregroundStyle(LoomColors.fgMuted)
            }
        }
        .padding()
        .background(LoomColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    private func metricColumn(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(.caption2)
                .foregroundStyle(LoomColors.fgMuted)
            Text(value)
                .font(.headline)
                .foregroundStyle(LoomColors.fgPrimary)
                .monospacedDigit()
        }
    }

    // MARK: - Tabs

    private var tabPicker: some View {
        Picker("Section", selection: $selectedTab) {
            ForEach(DetailTab.allCases, id: \.self) { tab in
                Label(tab.title, systemImage: tab.systemImage).tag(tab)
            }
        }
        .pickerStyle(.segmented)
        .padding(.horizontal)
    }

    @ViewBuilder
    private var tabContent: some View {
        switch selectedTab {
        case .tools: toolsTab
        case .files: filesTab
        case .errors: errorsTab
        case .usage: usageTab
        case .activity: activityTab
        }
    }

    // MARK: - Activity Tab

    private var activityTab: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader(title: "Live Activity", subtitle: "\(viewModel.liveEvents.count) events")

            if viewModel.liveEvents.isEmpty {
                emptyRow("No live activity recorded")
            } else {
                VStack(spacing: 0) {
                    let events = viewModel.liveEvents.reversed()
                    ForEach(Array(events.enumerated()), id: \.offset) { index, event in
                        activityRow(event)
                        if index < events.count - 1 {
                            Divider().background(LoomColors.border)
                        }
                    }
                }
                .background(LoomColors.bgSecondary)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            }
        }
        .padding(.horizontal)
    }

    private func activityRow(_ event: SSEEvent) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(event.type)
                    .font(.caption)
                    .foregroundStyle(LoomColors.accent)
                    .monospaced()
                Spacer()
            }
            Text(event.data)
                .font(.caption2)
                .foregroundStyle(LoomColors.fgPrimary)
                .lineLimit(4)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    // MARK: - Tools Tab

    private var toolsTab: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader(
                title: "Tool Calls",
                subtitle: toolsPage.map { "\($0.items.count) of \($0.total)" }
            )

            if isLoadingTools {
                ProgressView().frame(maxWidth: .infinity)
            } else if let page = toolsPage, !page.items.isEmpty {
                VStack(spacing: 0) {
                    ForEach(page.items) { tool in
                        toolRow(tool)
                        if tool.id != page.items.last?.id {
                            Divider().background(LoomColors.border)
                        }
                    }
                }
                .background(LoomColors.bgSecondary)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                emptyRow("No tool calls recorded")
            }
        }
        .padding(.horizontal)
    }

    private func toolRow(_ tool: SpawnToolCall) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: toolStatusIcon(tool))
                .foregroundStyle(toolStatusColor(tool))
                .font(.body)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(tool.name)
                        .font(.subheadline)
                        .foregroundStyle(LoomColors.fgPrimary)
                    if let server = tool.serverName, !server.isEmpty {
                        Text(server)
                            .font(.caption2)
                            .foregroundStyle(LoomColors.fgMuted)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(LoomColors.bgTertiary)
                            .clipShape(Capsule())
                    }
                }
                if let error = tool.error, !error.isEmpty {
                    Text(error)
                        .font(.caption2)
                        .foregroundStyle(LoomColors.statusCritical)
                        .lineLimit(2)
                }
            }
            Spacer()
            if let duration = tool.durationMs {
                Text(formatDuration(duration))
                    .font(.caption)
                    .foregroundStyle(LoomColors.fgSecondary)
                    .monospacedDigit()
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func toolStatusIcon(_ tool: SpawnToolCall) -> String {
        if tool.error != nil && !(tool.error ?? "").isEmpty { return "xmark.circle.fill" }
        if let code = tool.exitCode, code != 0 { return "xmark.circle.fill" }
        return "checkmark.circle.fill"
    }

    private func toolStatusColor(_ tool: SpawnToolCall) -> Color {
        if tool.error != nil && !(tool.error ?? "").isEmpty { return LoomColors.statusCritical }
        if let code = tool.exitCode, code != 0 { return LoomColors.statusCritical }
        return LoomColors.statusHealthy
    }

    // MARK: - Files Tab

    private var filesTab: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader(
                title: "File Changes",
                subtitle: filesPage.map { "\($0.items.count) of \($0.total)" }
            )

            if isLoadingFiles {
                ProgressView().frame(maxWidth: .infinity)
            } else if let page = filesPage, !page.items.isEmpty {
                VStack(spacing: 0) {
                    ForEach(page.items) { file in
                        fileRow(file)
                        if file.id != page.items.last?.id {
                            Divider().background(LoomColors.border)
                        }
                    }
                }
                .background(LoomColors.bgSecondary)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                emptyRow("No file changes recorded")
            }
        }
        .padding(.horizontal)
    }

    private func fileRow(_ file: SpawnFileChange) -> some View {
        HStack(spacing: 10) {
            Image(systemName: fileKindIcon(file.kind))
                .foregroundStyle(fileKindColor(file.kind))
            Text(file.path)
                .font(.caption)
                .monospaced()
                .foregroundStyle(LoomColors.fgPrimary)
                .lineLimit(2)
                .truncationMode(.middle)
            Spacer()
            Text(file.kind.uppercased())
                .font(.caption2)
                .foregroundStyle(fileKindColor(file.kind))
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(fileKindColor(file.kind).opacity(0.12))
                .clipShape(Capsule())
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func fileKindIcon(_ kind: String) -> String {
        switch kind.lowercased() {
        case "create": return "plus.circle"
        case "delete": return "minus.circle"
        default: return "pencil.circle"
        }
    }

    private func fileKindColor(_ kind: String) -> Color {
        switch kind.lowercased() {
        case "create": return LoomColors.statusHealthy
        case "delete": return LoomColors.statusCritical
        default: return LoomColors.info
        }
    }

    // MARK: - Errors Tab

    private var errorsTab: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader(
                title: "Agent Errors",
                subtitle: errorsPage.map { "\($0.items.count) of \($0.total)" }
            )

            if isLoadingErrors {
                ProgressView().frame(maxWidth: .infinity)
            } else if let page = errorsPage, !page.items.isEmpty {
                VStack(spacing: 0) {
                    ForEach(page.items) { err in
                        errorRow(err)
                        if err.id != page.items.last?.id {
                            Divider().background(LoomColors.border)
                        }
                    }
                }
                .background(LoomColors.bgSecondary)
                .clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                emptyRow("No errors recorded")
            }
        }
        .padding(.horizontal)
    }

    private func errorRow(_ err: SpawnAgentError) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(LoomColors.statusCritical)
            VStack(alignment: .leading, spacing: 2) {
                Text(err.type)
                    .font(.subheadline)
                    .foregroundStyle(LoomColors.fgPrimary)
                Text(err.message)
                    .font(.caption)
                    .foregroundStyle(LoomColors.fgSecondary)
                    .lineLimit(3)
                if let formatted = formatTime(err.time) {
                    Text(formatted)
                        .font(.caption2)
                        .foregroundStyle(LoomColors.fgMuted)
                }
            }
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    // MARK: - Usage Tab

    private var usageTab: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionHeader(title: "Token Usage", subtitle: nil)

            if let telemetry {
                VStack(spacing: 0) {
                    usageRow(label: "Input tokens", value: telemetry.tokenUsage.inputTokens)
                    Divider().background(LoomColors.border)
                    usageRow(label: "Output tokens", value: telemetry.tokenUsage.outputTokens)
                    Divider().background(LoomColors.border)
                    usageRow(label: "Cache creation", value: telemetry.tokenUsage.cacheCreationTokens)
                    Divider().background(LoomColors.border)
                    usageRow(label: "Cache read", value: telemetry.tokenUsage.cacheReadTokens)
                }
                .background(LoomColors.bgSecondary)
                .clipShape(RoundedRectangle(cornerRadius: 12))

                if let modelUsage = telemetry.modelUsage, !modelUsage.isEmpty {
                    sectionHeader(title: "Per-Model Usage", subtitle: nil)
                        .padding(.top, 8)
                    VStack(spacing: 0) {
                        let keys = modelUsage.keys.sorted()
                        ForEach(Array(keys.enumerated()), id: \.element) { index, key in
                            if let entry = modelUsage[key] {
                                modelRow(name: key, use: entry)
                                if index < keys.count - 1 {
                                    Divider().background(LoomColors.border)
                                }
                            }
                        }
                    }
                    .background(LoomColors.bgSecondary)
                    .clipShape(RoundedRectangle(cornerRadius: 12))
                }
            } else {
                emptyRow("No usage data yet")
            }
        }
        .padding(.horizontal)
    }

    private func usageRow(label: String, value: Int) -> some View {
        HStack {
            Text(label)
                .font(.caption)
                .foregroundStyle(LoomColors.fgSecondary)
            Spacer()
            Text(formatTokenCount(value))
                .font(.caption)
                .foregroundStyle(LoomColors.fgPrimary)
                .monospacedDigit()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func modelRow(name: String, use: SpawnModelUse) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(name)
                    .font(.caption)
                    .monospaced()
                    .foregroundStyle(LoomColors.fgPrimary)
                Spacer()
                Text(String(format: "$%.4f", use.costUSD))
                    .font(.caption)
                    .foregroundStyle(LoomColors.accent)
                    .monospacedDigit()
            }
            HStack(spacing: 8) {
                Text("in \(formatTokenCount(use.inputTokens))")
                Text("out \(formatTokenCount(use.outputTokens))")
            }
            .font(.caption2)
            .foregroundStyle(LoomColors.fgMuted)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    // MARK: - Shared helpers

    private func sectionHeader(title: String, subtitle: String?) -> some View {
        HStack {
            Text(title)
                .font(.subheadline)
                .foregroundStyle(LoomColors.fgPrimary)
            Spacer()
            if let subtitle {
                Text(subtitle)
                    .font(.caption2)
                    .foregroundStyle(LoomColors.fgMuted)
            }
        }
    }

    private func emptyRow(_ text: String) -> some View {
        Text(text)
            .font(.caption)
            .foregroundStyle(LoomColors.fgMuted)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding()
            .background(LoomColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    // MARK: - Loading

    private func loadAll() async {
        loadError = nil
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await loadTelemetry() }
            group.addTask { await loadTools() }
            group.addTask { await loadFiles() }
            group.addTask { await loadErrors() }
        }
    }

    private func loadTelemetry() async {
        isLoadingTelemetry = true
        defer { isLoadingTelemetry = false }
        do {
            let response: SpawnTelemetryResponse = try await apiClient.request(.spawnTelemetry(id: spawn.spawnId))
            telemetry = response.telemetry
        } catch let err as LoomAPIError {
            loadError = err.description
        } catch {
            loadError = error.localizedDescription
        }
    }

    private func loadTools() async {
        isLoadingTools = true
        defer { isLoadingTools = false }
        do {
            toolsPage = try await apiClient.request(.spawnTelemetryTools(id: spawn.spawnId, offset: 0, limit: 50))
        } catch {
            // Non-fatal — loadError surfaces the primary failure.
        }
    }

    private func loadFiles() async {
        isLoadingFiles = true
        defer { isLoadingFiles = false }
        do {
            filesPage = try await apiClient.request(.spawnTelemetryFiles(id: spawn.spawnId, offset: 0, limit: 50))
        } catch {
            // Non-fatal.
        }
    }

    private func loadErrors() async {
        isLoadingErrors = true
        defer { isLoadingErrors = false }
        do {
            errorsPage = try await apiClient.request(.spawnTelemetryErrors(id: spawn.spawnId, offset: 0, limit: 50))
        } catch {
            // Non-fatal.
        }
    }

    // MARK: - Formatting

    private func formatCost(_ cost: Double, estimated: Bool) -> String {
        let formatted = String(format: "$%.4f", cost)
        return estimated ? "~\(formatted)" : formatted
    }

    private func formatTokenCount(_ tokens: Int) -> String {
        if tokens >= 1_000_000 {
            return String(format: "%.1fM", Double(tokens) / 1_000_000.0)
        }
        if tokens >= 1_000 {
            return String(format: "%.1fk", Double(tokens) / 1_000.0)
        }
        return "\(tokens)"
    }

    private func formatDuration(_ ms: Int) -> String {
        if ms >= 1_000 {
            return String(format: "%.1fs", Double(ms) / 1000.0)
        }
        return "\(ms)ms"
    }

    private static let isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let isoFallback: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private func formatTime(_ iso: String) -> String? {
        let date = Self.isoFormatter.date(from: iso) ?? Self.isoFallback.date(from: iso)
        guard let date else { return nil }
        let formatter = DateFormatter()
        formatter.dateStyle = .none
        formatter.timeStyle = .medium
        return formatter.string(from: date)
    }
}

private func spawnDetailStatusColor(_ status: String) -> Color {
    switch status {
    case "running": return LoomColors.statusHealthy
    case "creating": return LoomColors.info
    case "completed": return LoomColors.fgMuted
    case "failed": return LoomColors.statusCritical
    case "stopped": return LoomColors.statusDegraded
    default: return LoomColors.fgMuted
    }
}
