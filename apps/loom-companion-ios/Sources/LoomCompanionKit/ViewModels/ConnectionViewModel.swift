import Foundation

/// ViewModel for login/pairing and connection management.
@Observable
public final class ConnectionViewModel {
    public var isAuthenticated = false
    public var isPairing = false
    public var pairingError: String?

    // Form fields
    public var baseURLInput: String = ""
    public var tokenInput: String = ""
    public var connectionMode: ConnectionMode = .lan

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
            isAuthenticated = true
        }
    }

    /// Attempt to pair with the Loom HUD instance.
    public func pair() async {
        guard !baseURLInput.isEmpty, !tokenInput.isEmpty else {
            pairingError = "Base URL and token are required"
            return
        }

        // Validate URL format
        guard let url = URL(string: baseURLInput) else {
            pairingError = "Invalid URL format"
            return
        }

        // Gateway mode requires HTTPS
        if connectionMode == .gateway, url.scheme != "https" {
            pairingError = "Gateway mode requires HTTPS"
            return
        }

        isPairing = true
        pairingError = nil
        defer { isPairing = false }

        let client = APIClient(baseURL: url, token: tokenInput)

        // Probe /ping to validate connection
        do {
            let _: PingResponse = try await client.request(.ping)
        } catch let error as LoomAPIError {
            switch error {
            case let .apiError(code, message, _):
                pairingError = "[\(code.rawValue)] \(message)"
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
        let profile = ConnectionProfile(name: "default", baseURL: baseURLInput, mode: connectionMode)
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
    }

    /// Build an APIClient from stored credentials.
    public func buildAPIClient() -> APIClient? {
        guard let token = tokenStore.loadToken(),
              let profile = tokenStore.loadProfile(),
              let url = URL(string: profile.baseURL)
        else {
            return nil
        }
        return APIClient(baseURL: url, token: token)
    }
}

struct PingResponse: Decodable {
    let pong: Bool
}
