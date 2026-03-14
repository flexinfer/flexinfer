import UIKit
import UserNotifications
import LoomCompanionKit

/// UIKit application delegate for push notification registration.
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {

    /// Shared API client, injected from the SwiftUI app lifecycle.
    var apiClient: (any LoomAPIClientProtocol)?

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        UNUserNotificationCenter.current().delegate = self

        UNUserNotificationCenter.current().requestAuthorization(
            options: [.alert, .badge, .sound]
        ) { granted, error in
            if let error {
                print("[AppDelegate] Push authorization error: \(error.localizedDescription)")
                return
            }
            guard granted else { return }
            DispatchQueue.main.async {
                application.registerForRemoteNotifications()
            }
        }
        return true
    }

    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        let tokenHex = deviceToken.map { String(format: "%02x", $0) }.joined()
        guard let client = apiClient else { return }

        Task {
            do {
                let _: PushRegistrationResponse = try await client.request(
                    .pushRegister(token: tokenHex, platform: .apns)
                )
            } catch {
                print("[AppDelegate] Push token registration failed: \(error)")
            }
        }
    }

    func application(
        _ application: UIApplication,
        didFailToRegisterForRemoteNotificationsWithError error: Error
    ) {
        print("[AppDelegate] Remote notification registration failed: \(error.localizedDescription)")
    }

    // MARK: - UNUserNotificationCenterDelegate

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .badge]
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse
    ) async {
        let userInfo = response.notification.request.content.userInfo
        guard let urlString = userInfo["deep_link"] as? String,
              let url = URL(string: urlString) else { return }

        await MainActor.run {
            // Post notification for the SwiftUI app to handle via onOpenURL.
            UIApplication.shared.open(url)
        }
    }
}
