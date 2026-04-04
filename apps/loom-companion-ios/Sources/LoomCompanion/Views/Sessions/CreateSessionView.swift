import SwiftUI
import LoomCompanionKit

struct CreateSessionView: View {
    @Environment(\.dismiss) private var dismiss
    @Bindable var viewModel: SessionsViewModel

    @State private var agentId = "claude-code"
    @State private var namespace = ""
    @State private var sessionDescription = ""
    @State private var autoRecall = true
    @State private var showCustomAgent = false
    @State private var namespaceSearch = ""

    private let agentTypes: [(id: String, label: String)] = [
        ("claude-code", "Claude Code"),
        ("gemini", "Gemini"),
        ("codex", "Codex"),
        ("kilocode", "Kilocode"),
        ("antigravity", "Antigravity"),
    ]

    private let columns = [
        GridItem(.flexible(), spacing: LoomSpacing.sm),
        GridItem(.flexible(), spacing: LoomSpacing.sm),
    ]

    var body: some View {
        NavigationStack {
            Form {
                agentSection
                namespaceSection
                detailsSection

                Section {
                    Toggle("Auto-recall context", isOn: $autoRecall)
                }

                if let error = viewModel.createError {
                    Section {
                        Label(error, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(LoomColors.statusCritical)
                    }
                }
            }
            .navigationTitle("New Session")
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if viewModel.isCreating {
                        ProgressView()
                    } else {
                        Button("Create") {
                            Task {
                                await viewModel.createSession(
                                    agentId: agentId,
                                    namespace: namespace.isEmpty ? nil : namespace,
                                    description: sessionDescription.isEmpty ? nil : sessionDescription,
                                    autoRecall: autoRecall
                                )
                                if viewModel.createError == nil {
                                    dismiss()
                                }
                            }
                        }
                        .disabled(agentId.trimmingCharacters(in: .whitespaces).isEmpty)
                    }
                }
            }
            .task {
                await viewModel.loadNamespaces()
            }
        }
    }

    // MARK: - Agent Section

    private var agentSection: some View {
        Section("Agent") {
            LazyVGrid(columns: columns, spacing: LoomSpacing.sm) {
                ForEach(agentTypes, id: \.id) { agent in
                    AgentTypeCard(
                        agentId: agent.id,
                        label: agent.label,
                        isSelected: !showCustomAgent && agentId == agent.id
                    ) {
                        showCustomAgent = false
                        agentId = agent.id
                    }
                }
            }

            if showCustomAgent {
                TextField("Custom agent ID", text: $agentId)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
                    .font(LoomTypography.monoCaption)
            } else {
                Button {
                    showCustomAgent = true
                    agentId = ""
                } label: {
                    Label("Custom agent ID...", systemImage: "pencil")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                }
            }
        }
    }

    // MARK: - Namespace Section

    private var namespaceSection: some View {
        Section("Namespace") {
            if !viewModel.availableNamespaces.isEmpty {
                TextField("Search or enter namespace", text: $namespaceSearch)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
                    .onChange(of: namespaceSearch) { _, newValue in
                        namespace = newValue
                    }

                let filtered = filteredNamespaces
                if !filtered.isEmpty {
                    ForEach(filtered.prefix(6)) { ns in
                        Button {
                            namespace = ns.namespace
                            namespaceSearch = ns.namespace
                        } label: {
                            HStack {
                                Text(ns.namespace)
                                    .font(LoomTypography.monoCaption)
                                    .foregroundStyle(LoomColors.textPrimary)
                                Spacer()
                                HStack(spacing: 4) {
                                    Text("\(ns.sessionCount)")
                                        .font(.caption2)
                                    Image(systemName: "doc.text")
                                        .font(.system(size: 8))
                                }
                                .foregroundStyle(LoomColors.textTertiary)
                                if ns.activeAgents > 0 {
                                    Circle()
                                        .fill(LoomColors.statusHealthy)
                                        .frame(width: 6, height: 6)
                                }
                            }
                        }
                        .listRowBackground(namespace == ns.namespace ? LoomColors.accent.opacity(0.08) : Color.clear)
                    }
                }
            } else {
                TextField("Namespace (optional)", text: $namespace)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
            }
        }
    }

    // MARK: - Details Section

    private var detailsSection: some View {
        Section("Details") {
            TextField("Description (optional)", text: $sessionDescription, axis: .vertical)
                .lineLimit(2...4)
        }
    }

    // MARK: - Helpers

    private var filteredNamespaces: [NamespaceSummary] {
        if namespaceSearch.isEmpty {
            return viewModel.availableNamespaces
        }
        let query = namespaceSearch.lowercased()
        return viewModel.availableNamespaces.filter {
            $0.namespace.lowercased().contains(query)
        }
    }
}

// MARK: - Agent Type Card

private struct AgentTypeCard: View {
    let agentId: String
    let label: String
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 4) {
                Image(systemName: LoomColors.agentTypeIcon(agentId))
                    .font(.system(size: 16))
                    .foregroundStyle(LoomColors.agentTypeColor(agentId))
                Text(label)
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textPrimary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, LoomSpacing.sm)
            .background(LoomColors.agentTypeColor(agentId).opacity(isSelected ? 0.15 : 0.05))
            .clipShape(RoundedRectangle(cornerRadius: 10))
            .overlay(
                RoundedRectangle(cornerRadius: 10)
                    .strokeBorder(
                        isSelected ? LoomColors.agentTypeColor(agentId) : Color.clear,
                        lineWidth: 2
                    )
            )
        }
        .buttonStyle(.plain)
        .overlay(alignment: .topTrailing) {
            if isSelected {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 12))
                    .foregroundStyle(LoomColors.agentTypeColor(agentId))
                    .padding(4)
            }
        }
    }
}
