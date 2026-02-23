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
}
