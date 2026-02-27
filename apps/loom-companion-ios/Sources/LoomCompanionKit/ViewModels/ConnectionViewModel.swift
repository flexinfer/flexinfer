import Foundation

/// ViewModel for login/pairing and connection management.
@Observable
public final class ConnectionViewModel {
    public var isAuthenticated = false
    public var isPairing = false
    public var pairingError: String?
    public var showLANPermissionHint = false

    // Form fields
    public var baseURLInput: String = ""
    public var tokenInput: String = ""
    public var connectionMode: ConnectionMode = .lan
    public var cloudflareAccessClientIDInput: String = ""
    public var cloudflareAccessClientSecretInput: String = ""

    @ObservationIgnored
    private let tokenStore: TokenStore

    @ObservationIgnored
    private var apiClient: APIClient?

    public init(tokenStore: TokenStore = TokenStore()) {
        self.tokenStore = tokenStore

        // Restore saved profile
        if let profile = tokenStore.loadProfile(), tokenStore.hasToken {
            baseURLInput = profile.baseURL
            connectionMode = profile.mode
            cloudflareAccessClientIDInput = profile.cloudflareAccessClientID ?? ""
            cloudflareAccessClientSecretInput = profile.cloudflareAccessClientSecret ?? ""
            isAuthenticated = true
        }
    }

    /// Attempt to pair with the Loom HUD instance.
    public func pair() async {
        guard !baseURLInput.isEmpty, !tokenInput.isEmpty else {
            pairingError = "Base URL and token are required"
            return
        }

        guard let normalized = Self.normalizedBaseURL(baseURLInput, mode: connectionMode) else {
            pairingError = "Invalid server URL"
            return
        }
        let url = normalized

        // Gateway mode requires HTTPS
        if connectionMode == .gateway, url.scheme != "https" {
            pairingError = "Gateway mode requires HTTPS"
            return
        }

        let cloudflareAccessClientID = normalizedOptional(cloudflareAccessClientIDInput)
        let cloudflareAccessClientSecret = normalizedOptional(cloudflareAccessClientSecretInput)
        if connectionMode == .gateway,
           (cloudflareAccessClientID == nil) != (cloudflareAccessClientSecret == nil)
        {
            pairingError = "Provide both CF-Access-Client-Id and CF-Access-Client-Secret, or leave both empty"
            return
        }

        isPairing = true
        pairingError = nil
        showLANPermissionHint = false
        defer { isPairing = false }

        let client = APIClient(
            baseURL: url,
            token: tokenInput,
            cloudflareAccessClientID: connectionMode == .gateway ? cloudflareAccessClientID : nil,
            cloudflareAccessClientSecret: connectionMode == .gateway ? cloudflareAccessClientSecret : nil
        )

        // Probe /ping to validate connection
        do {
            let _: PingResponse = try await client.request(.ping)
        } catch let error as LoomAPIError {
            switch error {
            case let .apiError(code, message, _):
                if code == .notFound, connectionMode == .gateway {
                    pairingError = "[not_found] mobile API route not configured on gateway (/api/mobile/v1 is not routed)"
                } else {
                    pairingError = "[\(code.rawValue)] \(message)"
                }
            case .networkError where connectionMode == .lan:
                pairingError = "Cannot reach server. If this is a local address, check that Local Network permission is enabled in Settings > Privacy & Security > Local Network."
                showLANPermissionHint = true
            case let .networkError(msg):
                pairingError = "Cannot reach server: \(msg)"
            default:
                pairingError = error.description
            }
            return
        } catch {
            pairingError = "Connection failed: \(error.localizedDescription)"
            return
        }

        // Save credentials
        let normalizedBaseURL = url.absoluteString
        baseURLInput = normalizedBaseURL
        let profile = ConnectionProfile(
            name: "default",
            baseURL: normalizedBaseURL,
            mode: connectionMode,
            cloudflareAccessClientID: connectionMode == .gateway ? cloudflareAccessClientID : nil,
            cloudflareAccessClientSecret: connectionMode == .gateway ? cloudflareAccessClientSecret : nil
        )
        do {
            try tokenStore.saveToken(tokenInput)
            try tokenStore.saveProfile(profile)
        } catch {
            pairingError = "Failed to save credentials"
            return
        }

        apiClient = client
        isAuthenticated = true
    }

    /// Log out and clear stored credentials.
    public func logout() {
        tokenStore.deleteToken()
        tokenStore.deleteProfile()
        apiClient = nil
        isAuthenticated = false
        tokenInput = ""
        cloudflareAccessClientIDInput = ""
        cloudflareAccessClientSecretInput = ""
    }

    /// Build an APIClient from stored credentials.
    public func buildAPIClient() -> APIClient? {
        guard let token = tokenStore.loadToken(),
              let profile = tokenStore.loadProfile(),
              let url = URL(string: profile.baseURL)
        else {
            return nil
        }
        return APIClient(
            baseURL: url,
            token: token,
            cloudflareAccessClientID: profile.cloudflareAccessClientID,
            cloudflareAccessClientSecret: profile.cloudflareAccessClientSecret
        )
    }

    static func normalizedBaseURL(_ input: String, mode: ConnectionMode) -> URL? {
        let trimmed = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return nil
        }

        let withScheme: String
        if trimmed.contains("://") {
            withScheme = trimmed
        } else {
            switch mode {
            case .lan:
                withScheme = "http://\(trimmed)"
            case .gateway:
                withScheme = "https://\(trimmed)"
            }
        }

        guard var components = URLComponents(string: withScheme),
              let host = components.host,
              !host.isEmpty
        else {
            return nil
        }

        if mode == .lan, components.port == nil {
            components.port = 3333
        }

        return components.url
    }

    private func normalizedOptional(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}

struct PingResponse: Decodable {
    let pong: Bool
}
