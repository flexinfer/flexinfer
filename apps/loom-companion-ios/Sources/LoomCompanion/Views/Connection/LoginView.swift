import SwiftUI
import LoomCompanionKit

struct LoginView: View {
    @Bindable var viewModel: ConnectionViewModel

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 24) {
                    // Header
                    VStack(spacing: 8) {
                        Image(systemName: "network.badge.shield.half.filled")
                            .font(.system(size: 48))
                            .foregroundStyle(.tint)
                        Text("Loom Companion")
                            .font(.title)
                            .fontWeight(.bold)
                        Text("Connect to your Loom HUD instance")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    .padding(.top, 40)

                    // Connection Mode
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Connection Mode")
                            .font(.subheadline)
                            .fontWeight(.medium)

                        Picker("Mode", selection: $viewModel.connectionMode) {
                            Text("LAN").tag(ConnectionMode.lan)
                            Text("Gateway").tag(ConnectionMode.gateway)
                        }
                        .pickerStyle(.segmented)

                        Text(modeDescription)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }

                    // Base URL
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Server URL")
                            .font(.subheadline)
                            .fontWeight(.medium)

                        TextField(urlPlaceholder, text: $viewModel.baseURLInput)
                            .textFieldStyle(.roundedBorder)
                            .textContentType(.URL)
                            .autocorrectionDisabled()
                            #if os(iOS)
                            .textInputAutocapitalization(.never)
                            .keyboardType(.URL)
                            #endif
                    }

                    // Bearer Token
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Bearer Token")
                            .font(.subheadline)
                            .fontWeight(.medium)

                        SecureField("Mobile operator token", text: $viewModel.tokenInput)
                            .textFieldStyle(.roundedBorder)
                            .autocorrectionDisabled()
                            #if os(iOS)
                            .textInputAutocapitalization(.never)
                            #endif
                    }

                    if viewModel.connectionMode == .gateway {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Cloudflare Access (Optional)")
                                .font(.subheadline)
                                .fontWeight(.medium)

                            TextField("CF-Access-Client-Id", text: $viewModel.cloudflareAccessClientIDInput)
                                .textFieldStyle(.roundedBorder)
                                .autocorrectionDisabled()
                                #if os(iOS)
                                .textInputAutocapitalization(.never)
                                #endif

                            SecureField("CF-Access-Client-Secret", text: $viewModel.cloudflareAccessClientSecretInput)
                                .textFieldStyle(.roundedBorder)
                                .autocorrectionDisabled()
                                #if os(iOS)
                                .textInputAutocapitalization(.never)
                                #endif

                            Text("Use these if your gateway is protected by Cloudflare Access service tokens.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }

                    // Error
                    if let error = viewModel.pairingError {
                        HStack {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .foregroundStyle(.red)
                            Text(error)
                                .font(.caption)
                                .foregroundStyle(.red)
                        }
                        .padding(12)
                        .background(.red.opacity(0.1))
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                    }

                    // LAN permission guidance after network failure
                    if viewModel.showLANPermissionHint {
                        LANPermissionView()
                    }

                    // Connect button
                    Button {
                        Task { await viewModel.pair() }
                    } label: {
                        if viewModel.isPairing {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                        } else {
                            Text("Connect")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(viewModel.isPairing || viewModel.baseURLInput.isEmpty || viewModel.tokenInput.isEmpty)
                }
                .padding()
            }
            .navigationTitle("Connect")
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
        }
    }

    private var modeDescription: String {
        switch viewModel.connectionMode {
        case .lan:
            #if targetEnvironment(simulator)
            return "Direct connection on your local network. In iOS Simulator, use http://127.0.0.1:3333 when HUD runs on this Mac."
            #else
            return "Direct connection on your local network. Requires local network permission on iOS."
            #endif
        case .gateway:
            return "Remote connection through a gateway. Requires HTTPS. Add Cloudflare Access service token fields if your gateway enforces Access."
        }
    }

    private var urlPlaceholder: String {
        switch viewModel.connectionMode {
        case .lan:
            #if targetEnvironment(simulator)
            return "http://127.0.0.1:3333"
            #else
            return "http://192.168.1.50:3333"
            #endif
        case .gateway:
            return "https://loom.example.com"
        }
    }
}
