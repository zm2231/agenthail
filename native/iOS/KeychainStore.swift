import Foundation
import Security

enum KeychainStore {
    private static let testStoreEnabled = ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil
    private static let testStoreLock = NSLock()
    private nonisolated(unsafe) static var testStore: [String: String] = [:]

    static func set(_ value: String, account: String) throws {
        if testStoreEnabled {
            testStoreLock.withLock { testStore[account] = value }
            return
        }
        let data = Data(value.utf8)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.agenthail.ios",
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
        var attributes = query
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        if PairedCredential.accounts.contains(account), let group = PairedCredential.accessGroup {
            var shared = attributes
            shared[kSecAttrAccessGroup as String] = group
            let status = SecItemAdd(shared as CFDictionary, nil)
            // An unsigned simulator build has no keychain entitlements; its
            // notifications then arrive without buttons.
            if status != errSecMissingEntitlement {
                guard status == errSecSuccess else { throw NSError(domain: NSOSStatusErrorDomain, code: Int(status)) }
                return
            }
        }
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else { throw NSError(domain: NSOSStatusErrorDomain, code: Int(status)) }
    }

    /// Moves a pairing saved before the notification service could read it
    /// into the shared group, so its notifications can offer buttons.
    static func shareCredential() {
        for account in PairedCredential.accounts {
            if let value = get(account) { try? set(value, account: account) }
        }
    }

    static func get(_ account: String) -> String? {
        if testStoreEnabled {
            return testStoreLock.withLock { testStore[account] }
        }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.agenthail.ios",
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func remove(_ account: String) {
        if testStoreEnabled {
            testStoreLock.withLock { testStore.removeValue(forKey: account) }
            return
        }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.agenthail.ios",
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
    }

    static func removeAll() {
        if testStoreEnabled {
            testStoreLock.withLock { testStore.removeAll() }
            return
        }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.agenthail.ios"
        ]
        SecItemDelete(query as CFDictionary)
    }
}
