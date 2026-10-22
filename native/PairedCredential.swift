import Foundation
import Security

/// A computer the iPhone is paired with: its host identity, address and
/// token. These are the only keychain items the notification service can
/// read: it needs them to fetch what a waiting request asks, which the push
/// never carries. Everything else stays in the app's own keychain group.
struct PairedHostCredential: Codable, Equatable {
    let hostID: String
    var hostName: String
    let endpoint: URL
    let token: String
    let pairedAt: Date
}

enum PairedCredential {
    static let service = "com.agenthail.ios"
    static let accountPrefix = "host:"

    static func account(for hostID: String) -> String { accountPrefix + hostID }
    static func isShared(_ account: String) -> Bool { account.hasPrefix(accountPrefix) }

    static var accessGroup: String? {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "AgenthailKeychainGroup") as? String,
              !group.isEmpty, !group.hasPrefix(".") else { return nil }
        return group
    }

    static func decode(_ value: String) -> PairedHostCredential? {
        try? JSONDecoder().decode(PairedHostCredential.self, from: Data(value.utf8))
    }

    static func encode(_ credential: PairedHostCredential) throws -> String {
        String(decoding: try JSONEncoder().encode(credential), as: UTF8.self)
    }

    /// The pairing a notification names by its host, as the notification
    /// service reads it.
    static func read(host hostID: String) -> PairedHostCredential? {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account(for: hostID),
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data,
              let credential = decode(String(decoding: data, as: UTF8.self)), credential.hostID == hostID else { return nil }
        return credential
    }
}
