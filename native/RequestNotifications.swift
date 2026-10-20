import Foundation
import UserNotifications

struct RequestNotificationAction: Codable, Equatable, Sendable {
    let id: String
    let title: String
}

struct RequestNotice: Equatable, Sendable {
    let prompt: String
    let actions: [RequestNotificationAction]
}

private struct OpenRequests: Decodable {
    struct Open: Decodable {
        let id: String
        let prompt: String?
        let actions: [RequestNotificationAction]?
    }

    let requests: [Open]
}

// Each request gets its own category because a category's buttons are shared
// by every notification that uses it.
enum RequestNotifications {
    static let categoryPrefix = "AGENTHAIL_REQ_"
    static let choices: Set<String> = ["allow", "always", "deny", "o0", "o1", "o2", "o3"]
    static let retainedRequests = 32

    static func categoryIdentifier(requestID: String) -> String { categoryPrefix + requestID }

    static func requestID(ofCategory identifier: String) -> String? {
        identifier.hasPrefix(categoryPrefix) ? String(identifier.dropFirst(categoryPrefix.count)) : nil
    }

    /// The request a waiting notification names, as this Mac describes it
    /// now: the line to show and the buttons that answer it. Nil when the
    /// request is no longer open or offers no buttons.
    static func notice(from data: Data, requestID: String) -> RequestNotice? {
        guard let state = try? JSONDecoder().decode(OpenRequests.self, from: data),
              let open = state.requests.first(where: { $0.id == requestID }),
              let actions = open.actions, !actions.isEmpty, actions.count <= 4,
              actions.allSatisfy({ choices.contains($0.id) && !$0.title.isEmpty }),
              Set(actions.map(\.id)).count == actions.count,
              let prompt = open.prompt?.trimmingCharacters(in: .whitespacesAndNewlines), !prompt.isEmpty else { return nil }
        return RequestNotice(prompt: prompt, actions: actions)
    }

    /// Reads a request from the paired Mac within fetchTimeout, well inside
    /// the time a notification service has before iOS shows the notification
    /// as it arrived.
    static func fetchNotice(endpoint: URL, token: String, sessionID: String, requestID: String, session: URLSession = fetchSession) async -> RequestNotice? {
        var components = URLComponents(url: endpoint, resolvingAgainstBaseURL: false)
        components?.path = "/api/v1/requests"
        components?.queryItems = [URLQueryItem(name: "sessionId", value: sessionID)]
        guard let url = components?.url else { return nil }
        var request = URLRequest(url: url, timeoutInterval: fetchTimeout)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        guard let (data, response) = try? await session.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
        return notice(from: data, requestID: requestID)
    }

    static let fetchTimeout: TimeInterval = 5

    static let fetchSession: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = fetchTimeout
        configuration.timeoutIntervalForResource = fetchTimeout
        configuration.waitsForConnectivity = false
        return URLSession(configuration: configuration)
    }()

    static func choice(forAction identifier: String) -> String? {
        choices.contains(identifier) ? identifier : nil
    }

    static func category(requestID: String, actions: [RequestNotificationAction]) -> UNNotificationCategory {
        let buttons = actions.map { action in
            UNNotificationAction(identifier: action.id, title: action.title, options: action.id == "deny" ? [.authenticationRequired, .destructive] : [.authenticationRequired])
        }
        return UNNotificationCategory(identifier: categoryIdentifier(requestID: requestID), actions: buttons, intentIdentifiers: [])
    }

    static func merged(existing: Set<UNNotificationCategory>, base: [UNNotificationCategory], adding category: UNNotificationCategory?, shown: [String]) -> Set<UNNotificationCategory> {
        let adding = category.flatMap { requestID(ofCategory: $0.identifier) }
        var retained: [String] = []
        for id in [adding].compactMap({ $0 }) + shown where !retained.contains(id) && retained.count < retainedRequests {
            retained.append(id)
        }
        let baseIDs = Set(base.map(\.identifier))
        var kept = Set(base)
        for existing in existing where !baseIDs.contains(existing.identifier) {
            guard let id = requestID(ofCategory: existing.identifier) else {
                kept.insert(existing)
                continue
            }
            if id != adding && retained.contains(id) { kept.insert(existing) }
        }
        if let category { kept.insert(category) }
        return kept
    }
}

// setNotificationCategories replaces the whole set and the framework has no
// call that adds one category, so each registration reads, merges and writes
// back. The actor serializes that within a process; a write another process
// overwrote is caught by reading the set back, and merged again.
actor RequestCategoryRegistry {
    static let shared = RequestCategoryRegistry()

    func register(requestID: String?, actions: [RequestNotificationAction], base: [UNNotificationCategory], center: UNUserNotificationCenter = .current()) async {
        let category = requestID.map { RequestNotifications.category(requestID: $0, actions: actions) }
        for _ in 0..<3 {
            let existing = await center.notificationCategories()
            let shown = await center.deliveredNotifications()
                .sorted { $0.date > $1.date }
                .compactMap { $0.request.content.userInfo["requestId"] as? String }
            center.setNotificationCategories(RequestNotifications.merged(existing: existing, base: base, adding: category, shown: shown))
            guard let category else { return }
            if await center.notificationCategories().contains(where: { $0.identifier == category.identifier }) { return }
        }
    }
}
