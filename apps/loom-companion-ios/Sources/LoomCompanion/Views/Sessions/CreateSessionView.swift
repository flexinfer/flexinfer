import SwiftUI
import LoomCompanionKit

struct CreateSessionView: View {
    @Environment(\.dismiss) private var dismiss
    @Bindable var viewModel: SessionsViewModel

    @State private var agentId = "claude-code"
    @State private var namespace = ""
    @State private var sessionDescription = ""
    @State private var autoRecall = true

    private let agentPresets = ["claude-code", "codex", "gemini"]

    var body: some View {
        NavigationStack {
            Form {
                Section("Agent") {
                    TextField("Agent ID", text: $agentId)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        #endif
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack {
                            ForEach(agentPresets, id: \.self) { preset in
                                Button(preset) {
                                    agentId = preset
                                }
                                .buttonStyle(.bordered)
                                .tint(agentId == preset ? .accentColor : .secondary)
                            }
                        }
                    }
                }

                Section("Details") {
                    TextField("Namespace (optional)", text: $namespace)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        #endif
                    TextField("Description (optional)", text: $sessionDescription, axis: .vertical)
                        .lineLimit(2...4)
                }

                Section {
                    Toggle("Auto-recall context", isOn: $autoRecall)
                }

                if let error = viewModel.createError {
                    Section {
                        Label(error, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
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
        }
    }
}
