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

/// Receipts go to the paired computer as each push arrives. One the phone
/// could not deliver, as when the computer is out of reach, waits in the
/// keychain group the notification service shares with the app, and the app
/// sends it later with the time it arrived.
enum PushReceipts {
    static let account = "push-receipts"
    static let kept = 50
    static let path = "/api/v1/device/push-receipts"

    static func alerts(authorization: UNAuthorizationStatus, alert: UNNotificationSetting) -> String {
        [.authorized, .provisional, .ephemeral].contains(authorization) && alert == .enabled ? "on" : "off"
    }

    static func body(_ receipts: [PushReceipt]) -> Data {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let rows = receipts.map { ["pushId": $0.pushId, "receivedAt": formatter.string(from: $0.receivedAt), "alerts": $0.alerts] }
        return (try? JSONSerialization.data(withJSONObject: ["receipts": rows])) ?? Data()
    }

    static func merged(_ pending: [PushReceipt], adding: [PushReceipt]) -> [PushReceipt] {
        var seen = Set<String>()
        let unique = (pending + adding).filter { seen.insert($0.host + "\u{0}" + $0.pushId).inserted }
        return Array(unique.suffix(kept))
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
            store(merged(pending(), adding: [receipt]))
        }
    }

    static func flush(credential: (String) -> PairedHostCredential? = PairedCredential.read(host:)) async {
        let waiting = pending()
        guard !waiting.isEmpty else { return }
        var unsent: [PushReceipt] = []
        for host in Set(waiting.map(\.host)).sorted() {
            let receipts = waiting.filter { $0.host == host }
            guard let paired = credential(host) else { continue }
            if await !post(receipts, endpoint: paired.endpoint, token: paired.token) { unsent += receipts }
        }
        store(merged(unsent, adding: pending().filter { !waiting.contains($0) }))
    }

    static func pending() -> [PushReceipt] {
        var query = item()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var found: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &found) == errSecSuccess, let data = found as? Data else { return [] }
        return (try? JSONDecoder().decode([PushReceipt].self, from: data)) ?? []
    }

    private static func store(_ receipts: [PushReceipt]) {
        SecItemDelete(item() as CFDictionary)
        guard !receipts.isEmpty, let data = try? JSONEncoder().encode(receipts) else { return }
        var attributes = item()
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(attributes as CFDictionary, nil)
    }

    private static func item() -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: PairedCredential.service,
            kSecAttrAccount as String: account
        ]
        if let group = PairedCredential.accessGroup { query[kSecAttrAccessGroup as String] = group }
        return query
    }
}
