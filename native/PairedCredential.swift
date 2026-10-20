import Foundation
import Security

// The paired Mac's address and token are the only keychain items the
// notification service can read: it needs them to fetch what a waiting
// request asks, which the push never carries. Everything else stays in the
// app's own keychain group.
enum PairedCredential {
    static let service = "com.agenthail.ios"
    static let accounts: Set<String> = ["endpoint", "token"]

    static var accessGroup: String? {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "AgenthailKeychainGroup") as? String,
              !group.isEmpty, !group.hasPrefix(".") else { return nil }
        return group
    }

    static func read() -> (endpoint: URL, token: String)? {
        guard let endpoint = value("endpoint").flatMap(URL.init(string:)), let token = value("token") else { return nil }
        return (endpoint, token)
    }

    private static func value(_ account: String) -> String? {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
