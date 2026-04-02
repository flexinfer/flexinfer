import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("ConnectionViewModel")
struct ConnectionViewModelTests {

    @Test("Initial state is not authenticated")
    func initialState() {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        #expect(vm.isAuthenticated == false)
        #expect(vm.isPairing == false)
        #expect(vm.pairingError == nil)
    }

    @Test("Pair fails with empty fields")
    func pairFailsEmpty() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = ""
        vm.tokenInput = ""
        await vm.pair()
        #expect(vm.pairingError == "Base URL and token are required")
        #expect(vm.isAuthenticated == false)
    }

    @Test("Pair fails with unreachable server")
    func pairFailsUnreachable() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "https://192.0.2.1:1" // TEST-NET, unreachable
        vm.tokenInput = "test-token"
        await vm.pair()
        #expect(vm.pairingError != nil)
        #expect(vm.isAuthenticated == false)
    }

    @Test("Gateway mode requires HTTPS")
    func gatewayRequiresHTTPS() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "http://example.com"
        vm.tokenInput = "test-token"
        vm.connectionMode = .gateway
        await vm.pair()
        #expect(vm.pairingError == "Gateway mode requires HTTPS")
    }

    @Test("Gateway mode accepts HTTPS URL")
    func gatewayAcceptsHTTPS() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "https://192.0.2.1:1" // TEST-NET, unreachable but HTTPS
        vm.tokenInput = "test-token"
        vm.connectionMode = .gateway
        await vm.pair()
        // Should fail with network error, NOT the HTTPS validation error
        #expect(vm.pairingError != nil)
        #expect(vm.pairingError != "Gateway mode requires HTTPS")
    }

    @Test("Gateway mode requires both Cloudflare Access fields when one is provided")
    func gatewayRequiresCompleteCloudflareAccessPair() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "https://mcp.flexinfer.ai"
        vm.tokenInput = "test-token"
        vm.connectionMode = .gateway
        vm.cloudflareAccessClientIDInput = "only-id"
        vm.cloudflareAccessClientSecretInput = ""

        await vm.pair()

        #expect(vm.pairingError == "Provide both CF-Access-Client-Id and CF-Access-Client-Secret, or leave both empty")
    }

    @Test("LAN mode allows HTTP")
    func lanAllowsHTTP() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "http://192.0.2.1:1" // TEST-NET, unreachable but HTTP
        vm.tokenInput = "test-token"
        vm.connectionMode = .lan
        await vm.pair()
        // Should fail with network error, NOT a scheme validation error
        #expect(vm.pairingError != nil)
        #expect(vm.pairingError != "Gateway mode requires HTTPS")
    }

    @Test("LAN mode network error shows permission hint")
    func lanNetworkErrorShowsPermissionHint() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "http://192.0.2.1:1" // TEST-NET, unreachable
        vm.tokenInput = "test-token"
        vm.connectionMode = .lan
        await vm.pair()
        #expect(vm.showLANPermissionHint == true)
        #expect(vm.pairingError?.contains("Local Network") == true)
    }

    @Test("Gateway mode network error does not show LAN permission hint")
    func gatewayNetworkErrorNoPermissionHint() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "https://192.0.2.1:1" // TEST-NET, unreachable
        vm.tokenInput = "test-token"
        vm.connectionMode = .gateway
        await vm.pair()
        #expect(vm.showLANPermissionHint == false)
    }

    @Test("LAN permission hint resets on new pair attempt")
    func lanPermissionHintResetsOnRetry() async {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.baseURLInput = "http://192.0.2.1:1"
        vm.tokenInput = "test-token"
        vm.connectionMode = .lan
        await vm.pair()
        #expect(vm.showLANPermissionHint == true)

        // Switch to gateway and retry — hint should clear
        vm.connectionMode = .gateway
        vm.baseURLInput = "https://192.0.2.1:1"
        await vm.pair()
        #expect(vm.showLANPermissionHint == false)
    }

    @Test("Logout clears state")
    func logout() {
        let vm = ConnectionViewModel(tokenStore: TokenStore())
        vm.isAuthenticated = true
        vm.tokenInput = "some-token"

        vm.logout()

        #expect(vm.isAuthenticated == false)
        #expect(vm.tokenInput == "")
    }

    @Test("LoomAPIError isAuthError")
    func apiErrorAuthCheck() {
        let authErr = LoomAPIError.apiError(code: .unauthorized, message: "bad", requestId: "r1")
        #expect(authErr.isAuthError == true)

        let revokedErr = LoomAPIError.apiError(code: .tokenRevoked, message: "revoked", requestId: "r2")
        #expect(revokedErr.isAuthError == true)

        let otherErr = LoomAPIError.apiError(code: .rateLimited, message: "limit", requestId: "r3")
        #expect(otherErr.isAuthError == false)
    }

    @Test("LoomAPIError isRateLimited")
    func apiErrorRateLimitCheck() {
        let err = LoomAPIError.apiError(code: .rateLimited, message: "limit", requestId: "r1")
        #expect(err.isRateLimited == true)

        let other = LoomAPIError.apiError(code: .unauthorized, message: "bad", requestId: "r2")
        #expect(other.isRateLimited == false)
    }

    @Test("LAN URL normalization adds default scheme and port")
    func lanURLNormalizationDefaults() {
        let url = ConnectionViewModel.normalizedBaseURL("192.168.50.176", mode: .lan)
        #expect(url?.absoluteString == "http://192.168.50.176:3333")
    }

    @Test("LAN URL normalization preserves explicit port")
    func lanURLNormalizationPreservesPort() {
        let url = ConnectionViewModel.normalizedBaseURL("http://192.168.50.176:8080", mode: .lan)
        #expect(url?.absoluteString == "http://192.168.50.176:8080")
    }

    @Test("Gateway URL normalization adds HTTPS scheme")
    func gatewayURLNormalizationDefaults() {
        let url = ConnectionViewModel.normalizedBaseURL("loom.example.com", mode: .gateway)
        #expect(url?.absoluteString == "https://loom.example.com")
    }

    @Test("URL normalization rejects invalid host")
    func urlNormalizationRejectsInvalidHost() {
        let url = ConnectionViewModel.normalizedBaseURL("http:///not-a-host", mode: .lan)
        #expect(url == nil)
    }

    @Test("Stored LAN connection preserves http and trims token")
    func restoredConnectionPreservesLANHTTP() {
        let profile = ConnectionProfile(
            name: "default",
            baseURL: "http://192.168.50.176:3333",
            mode: .lan
        )

        let restored = ConnectionViewModel.restoredConnection(profile: profile, rawToken: "  mobile-token  ")

        #expect(restored?.profile.baseURL == "http://192.168.50.176:3333")
        #expect(restored?.token == "mobile-token")
    }

    @Test("Stored LAN connection repairs legacy local https migration")
    func restoredConnectionRepairsLegacyLANHTTPS() {
        let profile = ConnectionProfile(
            name: "default",
            baseURL: "https://192.168.50.176:3333",
            mode: .lan
        )

        let restored = ConnectionViewModel.restoredConnection(profile: profile, rawToken: "mobile-token")

        #expect(restored?.profile.baseURL == "http://192.168.50.176:3333")
    }

    @Test("Stored connection rejects blank token")
    func restoredConnectionRejectsBlankToken() {
        let profile = ConnectionProfile(
            name: "default",
            baseURL: "http://192.168.50.176:3333",
            mode: .lan
        )

        let restored = ConnectionViewModel.restoredConnection(profile: profile, rawToken: "   ")

        #expect(restored == nil)
    }

    @Test("Dashboard error titles are specific")
    func dashboardErrorTitles() {
        #expect(LoomAPIError.noToken.dashboardTitle == "Reconnect Required")
        #expect(LoomAPIError.invalidURL(url: "bad").dashboardTitle == "Invalid Server URL")
        #expect(LoomAPIError.networkError(underlying: "offline").dashboardTitle == "Server Unreachable")
        #expect(LoomAPIError.apiError(code: .unauthorized, message: "bad token", requestId: "r1").dashboardTitle == "Authentication Failed")
        #expect(LoomAPIError.apiError(code: .forbidden, message: "denied", requestId: "r2").dashboardTitle == "Permission Denied")
        #expect(LoomAPIError.apiError(code: .notFound, message: "missing", requestId: "r3").dashboardTitle == "Mobile Route Missing")
    }
}
