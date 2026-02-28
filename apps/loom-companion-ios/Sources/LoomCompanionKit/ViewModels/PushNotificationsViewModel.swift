import Foundation

/// ViewModel for mobile alert policy inspection and push token registration.
@Observable
public final class PushNotificationsViewModel {
    public var pushToken = ""
    public var platform: PushPlatform = .apns

    public var isLoadingPolicy = false
    public var isRegistering = false
    public var isUnregistering = false

    public var policyVersion = ""
    public var policyEntries: [MobileAlertPolicyEntry] = []

    public var statusMessage: String?
    public var errorMessage: String?

    @ObservationIgnored
    private let apiClient: (any LoomAPIClientProtocol)?

    public init(apiClient: (any LoomAPIClientProtocol)?) {
        self.apiClient = apiClient
    }

    public func loadPolicy() async {
        guard let apiClient else {
            errorMessage = "No API client configured"
            return
        }

        isLoadingPolicy = true
        defer { isLoadingPolicy = false }
        errorMessage = nil

        do {
            let response: MobileAlertPolicyResponse = try await apiClient.request(.alertsPolicy)
            policyEntries = response.policy
            policyVersion = response.version
            statusMessage = "Loaded \(response.policy.count) policy entries"
        } catch {
            errorMessage = toLoomError(error).description
        }
    }

    public func registerPushToken() async {
        guard let apiClient else {
            errorMessage = "No API client configured"
            return
        }
        let trimmedToken = pushToken.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedToken.isEmpty else {
            errorMessage = "Push token is required"
            return
        }

        isRegistering = true
        defer { isRegistering = false }
        errorMessage = nil

        do {
            let response: PushRegistrationResponse = try await apiClient.request(.pushRegister(token: trimmedToken, platform: platform))
            statusMessage = response.registered
                ? "Push token registered (\(response.registrationId))"
                : "Push registration did not complete"
        } catch let err as LoomAPIError {
            if case let .apiError(code, _, _) = err, code == .notFound {
                errorMessage = "Push notifications are not enabled on this HUD"
            } else {
                errorMessage = err.description
            }
        } catch {
            errorMessage = toLoomError(error).description
        }
    }

    public func unregisterPushToken() async {
        guard let apiClient else {
            errorMessage = "No API client configured"
            return
        }
        let trimmedToken = pushToken.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedToken.isEmpty else {
            errorMessage = "Push token is required"
            return
        }

        isUnregistering = true
        defer { isUnregistering = false }
        errorMessage = nil

        do {
            let response: PushUnregisterResponse = try await apiClient.request(.pushUnregister(token: trimmedToken))
            statusMessage = response.removed
                ? "Push token removed"
                : "Push token was not registered"
        } catch let err as LoomAPIError {
            if case let .apiError(code, _, _) = err, code == .notFound {
                errorMessage = "Push notifications are not enabled on this HUD"
            } else {
                errorMessage = err.description
            }
        } catch {
            errorMessage = toLoomError(error).description
        }
    }

    private func toLoomError(_ error: Error) -> LoomAPIError {
        if let loomError = error as? LoomAPIError {
            return loomError
        }
        return .networkError(underlying: error.localizedDescription)
    }
}
