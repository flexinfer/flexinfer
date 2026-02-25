import Foundation
import Security
#if os(iOS)
import UIKit
#endif

/// Stable, privacy-preserving device identifier for the X-Device-ID header.
///
/// Best practice:
/// - Generate a random UUID (or seed with IDFV on iOS) and persist it in the Keychain.
/// - Use the Keychain so the value survives app reinstalls and remains per-device.
/// - Avoid hardware identifiers or host names which can change and/or be sensitive.
enum DeviceInfo {
    // Namespace the keychain entry by bundle identifier to avoid collisions.
    private static let service: String = {
        let bundleId = Bundle.main.bundleIdentifier ?? "app.device"
        return bundleId + ".x-device-id"
    }()
    private static let account = "device-id"
    private static var cachedId: String?

    /// Returns a stable identifier, generating and storing one if needed.
    static var deviceId: String {
        if let cachedId { return cachedId }

        if let existing = keychainRead(service: service, account: account) {
            cachedId = existing
            return existing
        }

        // Seed with IDFV on iOS if available, otherwise a fresh UUID.
        let seed: String
        #if os(iOS)
        seed = UIDevice.current.identifierForVendor?.uuidString ?? UUID().uuidString
        #else
        seed = UUID().uuidString
        #endif

        // Persist to keychain; even if it fails, return the seed so the app can proceed.
        _ = keychainSave(seed, service: service, account: account)
        cachedId = seed
        return seed
    }
}

// MARK: - Keychain helpers
private extension DeviceInfo {
    static func keychainRead(service: String, account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    @discardableResult
    static func keychainSave(_ value: String, service: String, account: String) -> Bool {
        let data = Data(value.utf8)

        let addQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
            // Per-device only; does not migrate to other devices via backup.
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        ]

        var status = SecItemAdd(addQuery as CFDictionary, nil)
        if status == errSecDuplicateItem {
            let findQuery: [String: Any] = [
                kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: service,
                kSecAttrAccount as String: account
            ]
            let attributesToUpdate: [String: Any] = [
                kSecValueData as String: data
            ]
            status = SecItemUpdate(findQuery as CFDictionary, attributesToUpdate as CFDictionary)
        }
        return status == errSecSuccess
    }
}
