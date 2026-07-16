import Foundation

enum AgenthailAPIError: LocalizedError {
    case unavailable(String)
    case incompatible(Int)
    case invalidResponse
    case request(Int, String)
    case streamClosed

    var errorDescription: String? {
        switch self {
        case .unavailable(let detail): return detail
        case .incompatible(let version): return "This Agenthail daemon uses protocol \(version). Update Agenthail to continue."
        case .invalidResponse: return "Agenthail returned an invalid response."
        case .request(_, let message): return message
        case .streamClosed: return "The Agenthail event stream disconnected."
        }
    }
}

final class AgenthailAPI: @unchecked Sendable {
    private let session: URLSession
    private let baseURL: URL
    private let token: String

    init(baseURL: URL, token: String, session: URLSession = .shared) {
        self.session = session
        self.baseURL = baseURL
        self.token = token
    }

#if os(macOS)
    convenience init(session: URLSession = .shared) throws {
        let environment = ProcessInfo.processInfo.environment
        if let rawURL = environment["AGENTHAIL_API_URL"],
           let baseURL = URL(string: rawURL),
           let token = environment["AGENTHAIL_API_TOKEN"],
           !token.isEmpty {
            self.init(baseURL: baseURL, token: token, session: session)
            return
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let tokenURL = home.appendingPathComponent(".agenthail/dashboard.token")
        let configURL = home.appendingPathComponent(".agenthail/dashboard.json")
        guard let token = try? String(contentsOf: tokenURL, encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines), !token.isEmpty else {
            throw AgenthailAPIError.unavailable("The Agenthail daemon has not created its access token yet.")
        }
        var listen = "127.0.0.1:7412"
        if let data = try? Data(contentsOf: configURL),
           let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let configured = object["listen"] as? String,
           !configured.isEmpty {
            listen = configured
        }
        guard let baseURL = URL(string: "http://\(listen)") else {
            throw AgenthailAPIError.unavailable("The Agenthail dashboard address is invalid.")
        }
        self.init(baseURL: baseURL, token: token, session: session)
    }
#endif

    func version() async throws -> APIVersion {
        let version: APIVersion = try await get("/api/v1/version")
        guard version.minimumProtocol <= 1, version.maximumProtocol >= 1 else {
            throw AgenthailAPIError.incompatible(version.protocolVersion)
        }
        return version
    }

    func snapshot(fresh: Bool = false) async throws -> DashboardSnapshot {
        try await get("/api/v1/snapshot" + (fresh ? "?fresh=1" : ""))
    }

    func sessionDetail(id: String) async throws -> SessionDetail {
        var components = URLComponents()
        components.path = "/api/v1/session"
        components.queryItems = [URLQueryItem(name: "id", value: id), URLQueryItem(name: "limit", value: "40")]
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        return try await get(path)
    }

    func devices() async throws -> [DeviceState] {
        let response: DeviceListResponse = try await get("/api/v1/devices")
        return response.devices
    }

    func settings() async throws -> DashboardSettingsState {
        try await get("/api/v1/settings")
    }

    func updateSettings(action: String) async throws {
        let timeout: TimeInterval = action == "notifications-enable" ? 130 : 25
        let _: EmptyResponse = try await request("/api/v1/settings", method: "POST", body: ["action": action], timeout: timeout)
    }

    func history(before: Int64 = 0, kind: String = "", query: String = "", limit: Int = 25) async throws -> HistoryPageResponse {
        var components = URLComponents()
        components.path = "/api/v1/history"
        components.queryItems = [URLQueryItem(name: "limit", value: String(limit))]
        if before > 0 { components.queryItems?.append(URLQueryItem(name: "before", value: String(before))) }
        if !kind.isEmpty { components.queryItems?.append(URLQueryItem(name: "kind", value: kind)) }
        if !query.isEmpty { components.queryItems?.append(URLQueryItem(name: "q", value: query)) }
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        return try await get(path)
    }

    func createPairing(name: String) async throws -> PairingResponse {
        try await post("/api/v1/pairings", body: ["name": name, "scopes": ["read", "control"]])
    }

    static func completePairing(endpoint: URL, secret: String, name: String, session: URLSession = .shared) async throws -> PairedDeviceResponse {
        var request = URLRequest(url: URL(string: "/api/v1/pair", relativeTo: endpoint)!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["secret": secret, "name": name])
        request.timeoutInterval = 25
        let (data, response) = try await session.data(for: request)
        guard let response = response as? HTTPURLResponse else { throw AgenthailAPIError.invalidResponse }
        guard (200..<300).contains(response.statusCode) else {
            let message = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])
                .flatMap { $0["error"] as? [String: String] }?["message"] ?? "Pairing failed."
            throw AgenthailAPIError.request(response.statusCode, message)
        }
        return try JSONDecoder().decode(PairedDeviceResponse.self, from: data)
    }

    func revokeDevice(id: String) async throws {
        let _: EmptyResponse = try await request("/api/v1/devices", method: "DELETE", body: ["id": id])
    }

    func configurePush(installationID: String, credential: String) async throws {
        let _: EmptyResponse = try await request("/api/v1/device/push", method: "PUT", body: ["installationId": installationID, "credential": credential])
    }

    func removePush() async throws {
        let _: EmptyResponse = try await request("/api/v1/device/push", method: "DELETE", body: nil)
    }

    func revokeCurrentDevice() async throws {
        let _: EmptyResponse = try await request("/api/v1/device", method: "DELETE", body: nil)
    }

    func action(_ action: String, sessionID: String? = nil, message: String? = nil, model: String? = nil, queueID: Int64? = nil, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, relayID: Int64? = nil) async throws {
        var body: [String: Any] = ["action": action]
        if let sessionID { body["sessionId"] = sessionID }
        if let message { body["message"] = message }
        if let model { body["model"] = model }
        if let queueID { body["queueId"] = queueID }
        if let channel { body["channel"] = channel }
        if let targetID { body["targetId"] = targetID }
        if let fromID { body["fromId"] = fromID }
        if let toID { body["toId"] = toID }
        if let pattern { body["pattern"] = pattern }
        if let relayID { body["relayId"] = relayID }
        let _: EmptyResponse = try await post("/api/v1/actions", body: body)
    }

    func streamEvents(after: UInt64, onConnected: @escaping @Sendable () async -> Void, onEvent: @escaping @Sendable (AgenthailEvent) async -> Void) async throws {
        var request = authorizedRequest(path: "/api/v1/events")
        request.setValue(String(after), forHTTPHeaderField: "Last-Event-ID")
        let (bytes, response) = try await session.bytes(for: request)
        try validate(response: response, data: nil)
        await onConnected()
        var dataLine = ""
        for try await line in bytes.lines {
            if Task.isCancelled { return }
            if line.hasPrefix("data: ") {
                dataLine = String(line.dropFirst(6))
            } else if line.isEmpty, !dataLine.isEmpty {
                if let data = dataLine.data(using: .utf8), let event = try? JSONDecoder().decode(AgenthailEvent.self, from: data) {
                    await onEvent(event)
                }
                dataLine = ""
            }
        }
        if !Task.isCancelled {
            throw AgenthailAPIError.streamClosed
        }
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        try await request(path, method: "GET", body: nil)
    }

    private func post<T: Decodable>(_ path: String, body: [String: Any]) async throws -> T {
        try await request(path, method: "POST", body: body)
    }

    private func request<T: Decodable>(_ path: String, method: String, body: [String: Any]?, timeout: TimeInterval = 25) async throws -> T {
        var request = authorizedRequest(path: path)
        request.httpMethod = method
        request.timeoutInterval = timeout
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        if T.self == EmptyResponse.self {
            return EmptyResponse() as! T
        }
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func authorizedRequest(path: String) -> URLRequest {
        var request = URLRequest(url: URL(string: path, relativeTo: baseURL)!)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.timeoutInterval = 25
        return request
    }

    private func validate(response: URLResponse, data: Data?) throws {
        guard let response = response as? HTTPURLResponse else { throw AgenthailAPIError.invalidResponse }
        guard (200..<300).contains(response.statusCode) else {
            var message = HTTPURLResponse.localizedString(forStatusCode: response.statusCode)
            if let data, let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                if let error = object["error"] as? [String: String], let detail = error["message"] {
                    message = detail
                }
            }
            throw AgenthailAPIError.request(response.statusCode, message)
        }
    }

}

private struct EmptyResponse: Decodable {}
