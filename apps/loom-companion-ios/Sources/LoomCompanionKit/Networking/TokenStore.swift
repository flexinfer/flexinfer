import Foundation
#if canImport(Security)
import Security
#endif

/// Keychain-backed storage for the mobile bearer token.
public final class TokenStore: Sendable {
    private static let service = "com.loom.companion"
    private static let tokenAccount = "mobile-operator-token"
    private static let profileAccount = "connection-profile"

    public init() {}

    // MARK: - Token

    public func saveToken(_ token: String) throws {
        let data = Data(token.utf8)
        try saveKeychainItem(account: Self.tokenAccount, data: data)
    }

    public func loadToken() -> String? {
        guard let data = loadKeychainItem(account: Self.tokenAccount) else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public func deleteToken() {
        deleteKeychainItem(account: Self.tokenAccount)
    }

    public var hasToken: Bool {
        loadToken() != nil
    }

    // MARK: - Connection Profile

    public func saveProfile(_ profile: ConnectionProfile) throws {
        let data = try JSONEncoder().encode(profile)
        try saveKeychainItem(account: Self.profileAccount, data: data)
    }

    public func loadProfile() -> ConnectionProfile? {
        guard let data = loadKeychainItem(account: Self.profileAccount) else { return nil }
        return try? JSONDecoder().decode(ConnectionProfile.self, from: data)
    }

    public func deleteProfile() {
        deleteKeychainItem(account: Self.profileAccount)
    }

    // MARK: - Keychain Operations

    private func saveKeychainItem(account: String, data: Data) throws {
        #if canImport(Security)
        // Delete existing item first
        let deleteQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(deleteQuery as CFDictionary)

        // Add new item
        let addQuery: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
        ]
        let status = SecItemAdd(addQuery as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw KeychainError.saveFailed(status: status)
        }
        #endif
    }

    private func loadKeychainItem(account: String) -> Data? {
        #if canImport(Security)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: AnyObject?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess else { return nil }
        return result as? Data
        #else
        return nil
        #endif
    }

    private func deleteKeychainItem(account: String) {
        #if canImport(Security)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
        #endif
    }
}

enum KeychainError: Error {
    case saveFailed(status: OSStatus)
}
