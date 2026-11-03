import Foundation
import Security
import UserNotifications

/// That a push reached this phone, for the computer that sent it.
struct PushReceipt: Codable, Equatable, Sendable {
    let host: String
    let pushId: String
    let receivedAt: Date
    let alerts: String
}

/// An undelivered receipt waits as its own keychain item, so the notification
/// service and the app only add or delete items and never overwrite each other.
enum PushReceipts {
    static let accountPrefix = "push-receipt:"
    static let kept = 50
    static let path = "/api/v1/device/push-receipts"

    static func alerts(authorization: UNAuthorizationStatus, alert: UNNotificationSetting) -> String {
        [.authorized, .provisional, .ephemeral].contains(authorization) && alert == .enabled ? "on" : "off"
    }

    static func account(_ receipt: PushReceipt) -> String { accountPrefix + receipt.host + ":" + receipt.pushId }

    static func body(_ receipts: [PushReceipt]) -> Data {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let rows = receipts.map { ["pushId": $0.pushId, "receivedAt": formatter.string(from: $0.receivedAt), "alerts": $0.alerts] }
        return (try? JSONSerialization.data(withJSONObject: ["receipts": rows])) ?? Data()
    }

    static func overflow(_ receipts: [PushReceipt]) -> [PushReceipt] {
        Array(receipts.sorted { $0.receivedAt < $1.receivedAt }.dropLast(kept))
    }

    static func post(_ receipts: [PushReceipt], endpoint: URL, token: String, session: URLSession = RequestNotifications.fetchSession) async -> Bool {
        guard !receipts.isEmpty, var components = URLComponents(url: endpoint, resolvingAgainstBaseURL: false) else { return receipts.isEmpty }
        components.path = path
        components.query = nil
        guard let url = components.url else { return false }
        var request = URLRequest(url: url, timeoutInterval: RequestNotifications.fetchTimeout)
        request.httpMethod = "POST"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = body(receipts)
        guard let (_, response) = try? await session.data(for: request) else { return false }
        return (response as? HTTPURLResponse)?.statusCode == 200
    }

    static func send(_ receipt: PushReceipt, credential: PairedHostCredential) async {
        if await !post([receipt], endpoint: credential.endpoint, token: credential.token) {
            enqueue(receipt)
        }
    }

    static func flush(credential: (String) -> PairedCredential.Lookup = PairedCredential.lookup(host:), session: URLSession = RequestNotifications.fetchSession) async {
        let waiting = pending()
        for host in Set(waiting.map(\.host)).sorted() {
            let receipts = waiting.filter { $0.host == host }
            let paired: PairedHostCredential
            switch credential(host) {
            case .found(let found): paired = found
            case .absent:
                receipts.forEach(remove)
                continue
            case .unavailable: continue
            }
            if await post(receipts, endpoint: paired.endpoint, token: paired.token, session: session) {
                receipts.forEach(remove)
            }
        }
    }

    static func enqueue(_ receipt: PushReceipt) {
        guard let data = try? JSONEncoder().encode(receipt) else { return }
        var attributes = item(account(receipt))
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(attributes as CFDictionary, nil)
        overflow(pending()).forEach(remove)
    }

    static func pending() -> [PushReceipt] {
        var query = item(nil)
        query[kSecReturnAttributes as String] = true
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitAll
        var found: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &found) == errSecSuccess, let rows = found as? [[String: Any]] else { return [] }
        return rows.compactMap { row in
            guard let account = row[kSecAttrAccount as String] as? String, account.hasPrefix(accountPrefix),
                  let data = row[kSecValueData as String] as? Data else { return nil }
            return try? JSONDecoder().decode(PushReceipt.self, from: data)
        }
    }

    static func forget(host: String) {
        pending().filter { $0.host == host }.forEach(remove)
    }

    static func remove(_ receipt: PushReceipt) {
        SecItemDelete(item(account(receipt)) as CFDictionary)
    }

    private static func item(_ account: String?) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: PairedCredential.service
        ]
        if let account { query[kSecAttrAccount as String] = account }
        if let group = PairedCredential.accessGroup { query[kSecAttrAccessGroup as String] = group }
        return query
    }
}
