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
        if PairedCredential.isShared(account), let group = PairedCredential.accessGroup {
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

    static func accounts(prefix: String) -> [String] {
        if testStoreEnabled {
            return testStoreLock.withLock { testStore.keys.filter { $0.hasPrefix(prefix) }.sorted() }
        }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.agenthail.ios",
            kSecReturnAttributes as String: true,
            kSecMatchLimit as String: kSecMatchLimitAll
        ]
        var items: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &items) == errSecSuccess,
              let rows = items as? [[String: Any]] else { return [] }
        return rows.compactMap { $0[kSecAttrAccount as String] as? String }.filter { $0.hasPrefix(prefix) }.sorted()
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

extension PairedCredential {
    static func all() -> [PairedHostCredential] {
        KeychainStore.accounts(prefix: accountPrefix)
            .compactMap { account in KeychainStore.get(account).flatMap(decode).flatMap { self.account(for: $0.hostID) == account ? $0 : nil } }
            .sorted { $0.pairedAt < $1.pairedAt }
    }

    static func save(_ credential: PairedHostCredential) throws {
        try KeychainStore.set(try encode(credential), account: account(for: credential.hostID))
    }
}
