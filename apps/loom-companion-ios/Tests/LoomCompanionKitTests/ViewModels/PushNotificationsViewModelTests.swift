import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("PushNotificationsViewModel")
struct PushNotificationsViewModelTests {

    @Test("Loads alert policy entries")
    func loadsAlertPolicyEntries() async {
        let mock = MockAPIClient()
        mock.alertPolicyResponse = MobileAlertPolicyResponse(
            policy: [
                MobileAlertPolicyEntry(
                    eventType: "agent.session.start",
                    severity: "info",
                    interruptionLevel: "passive",
                    title: "Session Started",
                    allowedActions: ["view_session", "acknowledge"],
                    conditional: false
                ),
            ],
            version: "v1"
        )
        let vm = PushNotificationsViewModel(apiClient: mock)

        await vm.loadPolicy()

        #expect(vm.errorMessage == nil)
        #expect(vm.policyVersion == "v1")
        #expect(vm.policyEntries.count == 1)
        #expect(vm.policyEntries.first?.eventType == "agent.session.start")
    }

    @Test("Registers push token successfully")
    func registersPushTokenSuccessfully() async {
        let mock = MockAPIClient()
        mock.pushRegistrationResponse = PushRegistrationResponse(registered: true, registrationId: "reg_test")
        let vm = PushNotificationsViewModel(apiClient: mock)
        vm.pushToken = "tok_123"
        vm.platform = .apns

        await vm.registerPushToken()

        #expect(vm.errorMessage == nil)
        #expect(vm.statusMessage?.contains("reg_test") == true)
    }

    @Test("Unregisters push token successfully")
    func unregistersPushTokenSuccessfully() async {
        let mock = MockAPIClient()
        mock.pushUnregisterResponse = PushUnregisterResponse(removed: true)
        let vm = PushNotificationsViewModel(apiClient: mock)
        vm.pushToken = "tok_123"

        await vm.unregisterPushToken()

        #expect(vm.errorMessage == nil)
        #expect(vm.statusMessage == "Push token removed")
    }

    @Test("Not found push endpoint surfaces disabled message")
    func pushNotFoundShowsFeatureDisabledMessage() async {
        let mock = MockAPIClient()
        mock.endpointFailures["/api/mobile/v1/push/register"] = .apiError(
            code: .notFound,
            message: "push notifications are not enabled",
            requestId: "req_1"
        )
        let vm = PushNotificationsViewModel(apiClient: mock)
        vm.pushToken = "tok_123"

        await vm.registerPushToken()

        #expect(vm.errorMessage == "Push notifications are not enabled on this HUD")
    }

    @Test("Missing token blocks registration request")
    func missingTokenBlocksRegistration() async {
        let mock = MockAPIClient()
        let vm = PushNotificationsViewModel(apiClient: mock)
        vm.pushToken = "   "

        await vm.registerPushToken()

        #expect(vm.errorMessage == "Push token is required")
    }
}
