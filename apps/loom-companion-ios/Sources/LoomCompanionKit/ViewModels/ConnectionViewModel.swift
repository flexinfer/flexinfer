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

    struct RestoredConnectionState: Equatable {
        let profile: ConnectionProfile
        let token: String
    }

    public init(tokenStore: TokenStore = TokenStore()) {
        self.tokenStore = tokenStore

        if let profile = tokenStore.loadProfile() {
            let normalizedProfile = Self.normalizedStoredProfile(profile)
            if normalizedProfile != profile {
                try? tokenStore.saveProfile(normalizedProfile)
            }

            baseURLInput = normalizedProfile.baseURL
            connectionMode = normalizedProfile.mode
            cloudflareAccessClientIDInput = normalizedProfile.cloudflareAccessClientID ?? ""
            cloudflareAccessClientSecretInput = normalizedProfile.cloudflareAccessClientSecret ?? ""
            isAuthenticated = Self.restoredConnection(profile: normalizedProfile, rawToken: tokenStore.loadToken()) != nil
        }
    }

    /// Attempt to pair with the Loom HUD instance.
    public func pair() async {
        let trimmedToken = tokenInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !baseURLInput.isEmpty, !trimmedToken.isEmpty else {
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
            token: trimmedToken,
            cloudflareAccessClientID: connectionMode == .gateway ? cloudflareAccessClientID : nil,
            cloudflareAccessClientSecret: connectionMode == .gateway ? cloudflareAccessClientSecret : nil,
            allowsInsecureTLS: connectionMode == .lan
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
        tokenInput = trimmedToken
        let profile = ConnectionProfile(
            name: "default",
            baseURL: normalizedBaseURL,
            mode: connectionMode,
            cloudflareAccessClientID: connectionMode == .gateway ? cloudflareAccessClientID : nil,
            cloudflareAccessClientSecret: connectionMode == .gateway ? cloudflareAccessClientSecret : nil
        )
        do {
            try tokenStore.saveToken(trimmedToken)
            try tokenStore.saveProfile(profile)
        } catch {
            pairingError = "Failed to save credentials"
            return
        }

        apiClient = client
        isAuthenticated = true
    }

    /// Apply a one-shot configuration payload delivered via the
    /// `loom://configure` deep link. Sets form fields from the payload, then
    /// runs the normal `pair()` flow so the same validation, probe, and
    /// keychain persistence paths apply.
    ///
    /// Always resets any existing auth first — on iOS the keychain survives
    /// app uninstall, so a stale profile from a prior install would otherwise
    /// shadow the new credentials. The caller has already signaled intent
    /// (by invoking `make mobile-app-run-device` with fresh creds), so wiping
    /// stored state is the right default.
    ///
    /// This is the mechanism `make mobile-app-run-device` uses to seed a
    /// freshly installed dev build without making the user retype secrets
    /// on the Connect screen.
    public func applyConfigureSpec(_ spec: DeepLink.ConfigureSpec) async {
        logout()
        baseURLInput = spec.url
        tokenInput = spec.bearer
        connectionMode = (spec.mode == "lan") ? .lan : .gateway
        cloudflareAccessClientIDInput = spec.cfClientID ?? ""
        cloudflareAccessClientSecretInput = spec.cfClientSecret ?? ""
        await pair()
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
        guard let profile = tokenStore.loadProfile(),
              let restored = Self.restoredConnection(profile: profile, rawToken: tokenStore.loadToken()),
              let url = URL(string: restored.profile.baseURL)
        else {
            return nil
        }
        if restored.profile != profile {
            try? tokenStore.saveProfile(restored.profile)
        }
        return APIClient(
            baseURL: url,
            token: restored.token,
            cloudflareAccessClientID: restored.profile.cloudflareAccessClientID,
            cloudflareAccessClientSecret: restored.profile.cloudflareAccessClientSecret,
            allowsInsecureTLS: restored.profile.mode == .lan
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
            withScheme = mode == .gateway ? "https://\(trimmed)" : "http://\(trimmed)"
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

    static func restoredConnection(profile: ConnectionProfile, rawToken: String?) -> RestoredConnectionState? {
        guard let token = rawToken?.trimmingCharacters(in: .whitespacesAndNewlines),
              !token.isEmpty
        else {
            return nil
        }

        let normalizedProfile = normalizedStoredProfile(profile)
        guard !normalizedProfile.baseURL.isEmpty else {
            return nil
        }

        return RestoredConnectionState(profile: normalizedProfile, token: token)
    }

    static func normalizedStoredProfile(_ profile: ConnectionProfile) -> ConnectionProfile {
        let repairedBaseURL = repairedLegacyLANBaseURL(profile)
        guard let normalizedURL = normalizedBaseURL(repairedBaseURL, mode: profile.mode) else {
            return profile
        }

        return ConnectionProfile(
            name: profile.name,
            baseURL: normalizedURL.absoluteString,
            mode: profile.mode,
            cloudflareAccessClientID: profile.cloudflareAccessClientID,
            cloudflareAccessClientSecret: profile.cloudflareAccessClientSecret
        )
    }

    private static func repairedLegacyLANBaseURL(_ profile: ConnectionProfile) -> String {
        guard profile.mode == .lan,
              let url = URL(string: profile.baseURL),
              url.scheme?.lowercased() == "https",
              let host = url.host,
              hostLooksLocal(host)
        else {
            return profile.baseURL
        }

        var components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        components?.scheme = "http"
        return components?.url?.absoluteString ?? profile.baseURL
    }

    private static func hostLooksLocal(_ host: String) -> Bool {
        let normalized = host.lowercased()
        if normalized == "localhost" || normalized == "::1" || normalized == "127.0.0.1" || normalized.hasSuffix(".local") {
            return true
        }

        let octets = normalized.split(separator: ".").compactMap { Int($0) }
        guard octets.count == 4 else {
            return false
        }

        if octets[0] == 10 {
            return true
        }
        if octets[0] == 192, octets[1] == 168 {
            return true
        }
        if octets[0] == 172, (16 ... 31).contains(octets[1]) {
            return true
        }

        return false
    }
}

struct PingResponse: Decodable {
    let pong: Bool
}
