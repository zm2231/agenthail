import Foundation

enum AgenthailAPIError: LocalizedError {
    case unavailable(String)
    case incompatible(Int)
    case invalidResponse
    case request(Int, String)
    case historyGap(String)
    case streamGap
    case streamClosed

    var errorDescription: String? {
        switch self {
        case .unavailable(let detail): return detail
        case .incompatible: return "Agenthail needs an update before this app can reconnect."
        case .invalidResponse: return "Agenthail returned an invalid response."
        case .request(_, let message): return message
        case .historyGap(let message): return message
        case .streamGap: return "The live activity history changed. Reloading the current activity."
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
            throw AgenthailAPIError.unavailable("Agenthail is still starting. Try again in a moment.")
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

    func sessionDetail(id: String, includeTimeline: Bool = false, timelineBefore: Int64? = nil) async throws -> SessionDetail {
        var components = URLComponents()
        components.path = "/api/v1/session"
        components.queryItems = [URLQueryItem(name: "id", value: id), URLQueryItem(name: "limit", value: "40")]
        if includeTimeline { components.queryItems?.append(URLQueryItem(name: "timeline", value: "1")) }
        if let timelineBefore { components.queryItems?.append(URLQueryItem(name: "timelineBefore", value: String(timelineBefore))) }
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        return try await get(path)
    }

    func sessionMetadata(id: String) async throws -> SessionMetadata {
        var components = URLComponents()
        components.path = "/api/v1/session-metadata"
        components.queryItems = [URLQueryItem(name: "id", value: id)]
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        return try await get(path)
    }

    func sessionAttachment(sessionID: String, id: String) async throws -> Data {
        var components = URLComponents()
        components.path = "/api/v1/session-attachment"
        components.queryItems = [URLQueryItem(name: "sessionId", value: sessionID), URLQueryItem(name: "id", value: id)]
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        let (bytes, response) = try await session.bytes(for: authorizedRequest(path: path))
        guard let response = response as? HTTPURLResponse else { throw AgenthailAPIError.invalidResponse }
        guard response.statusCode == 200 else {
            if response.statusCode == 413 { throw AgenthailAPIError.request(413, "This image is too large to display on the device.") }
            throw AgenthailAPIError.request(response.statusCode, HTTPURLResponse.localizedString(forStatusCode: response.statusCode))
        }
        if let length = response.value(forHTTPHeaderField: "Content-Length"), let length = Int(length), length > 10 * 1024 * 1024 {
            throw AgenthailAPIError.request(413, "This image is too large to display on the device.")
        }
        var data = Data()
        for try await byte in bytes {
            if Task.isCancelled { throw CancellationError() }
            data.append(byte)
            if data.count > 10 * 1024 * 1024 { throw AgenthailAPIError.request(413, "This image is too large to display on the device.") }
        }
        return data
    }

    func sendInstruction(action: String, sessionID: String, message: String, turnSettings: TurnSettings = .init(), idempotencyKey: String? = nil) async throws -> ActionReceipt {
        let body = InstructionRequest(action: action, sessionID: sessionID, message: message, turnSettings: turnSettings)
        return try await requestEncoded("/api/v1/actions", method: "POST", body: body, idempotencyKey: idempotencyKey)
    }

    func sessionOptions() async throws -> SessionCreationOptions { try await get("/api/v1/session-options") }
    func queuedInstructions() async throws -> [QueueState] {
        let response: QueueResponse = try await get("/api/v1/queue")
        return response.items
    }

    func nameSession(id: String, alias: String) async throws {
        let _: EmptyResponse = try await post("/api/v1/actions", body: ["action": "alias", "sessionId": id, "alias": alias])
    }

    func creationModels(surface: String) async throws -> [ModelOption] {
        var components = URLComponents()
        components.path = "/api/v1/models"
        components.queryItems = [URLQueryItem(name: "surface", value: surface)]
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        let response: CreationModels = try await get(path)
        return response.models
    }

    func createSession(surface: String, message: String, cwd: String, model: String, turnSettings: TurnSettings = .init(), claude: ClaudeCreationSettings = .init(), launcher: String? = nil, idempotencyKey: String? = nil) async throws -> SessionCreationReceipt {
        if launcher != nil && (!turnSettings.isEmpty || !claude.fields.isEmpty) {
            throw AgenthailAPIError.unavailable("Terminal sessions do not support advanced launch settings.")
        }
        if surface == "codex" {
            let body = SessionCreateRequest(action: "session-create", surface: surface, message: message, cwd: cwd, model: model, turnSettings: turnSettings, launcher: launcher)
            return try await requestEncoded("/api/v1/actions", method: "POST", body: body, timeout: 65, idempotencyKey: idempotencyKey)
        }
        var body = ["action": surface == "notion" ? "notion-create" : "session-create", "surface": surface, "message": message, "cwd": cwd, "model": model]
        if surface == "claude" { body.merge(claude.fields) { _, value in value } }
        if let launcher { body["launcher"] = launcher }
        return try await request("/api/v1/actions", method: "POST", body: body, timeout: 65, idempotencyKey: idempotencyKey)
    }

    func searchSessions(query: String) async throws -> SessionSearchResponse {
        var components = URLComponents()
        components.path = "/api/v1/search"
        components.queryItems = [URLQueryItem(name: "surface", value: "codex"), URLQueryItem(name: "q", value: query)]
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

    func action(_ action: String, sessionID: String? = nil, message: String? = nil, model: String? = nil, queueID: Int64? = nil, deliveryID: Int64? = nil, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, relayID: Int64? = nil) async throws {
        var body: [String: Any] = ["action": action]
        if let sessionID { body["sessionId"] = sessionID }
        if let message { body["message"] = message }
        if let model { body["model"] = model }
        if let queueID { body["queueId"] = queueID }
        if let deliveryID { body["deliveryId"] = deliveryID }
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

    func streamSession(id: String, after: UInt64, onConnected: @escaping @Sendable () async -> Void, onEvent: @escaping @Sendable (SessionStreamEvent) async -> Void) async throws {
        var components = URLComponents()
        components.path = "/api/v1/session-stream"
        components.queryItems = [URLQueryItem(name: "id", value: id), URLQueryItem(name: "after", value: String(after))]
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        var request = authorizedRequest(path: path)
        request.setValue(String(after), forHTTPHeaderField: "Last-Event-ID")
        let (bytes, response) = try await session.bytes(for: request)
        if let response = response as? HTTPURLResponse, response.statusCode == 409 {
            throw AgenthailAPIError.streamGap
        }
        try validate(response: response, data: nil)
        await onConnected()
        var dataLine = ""
        for try await line in bytes.lines {
            if Task.isCancelled { return }
            if line.hasPrefix("data: ") {
                dataLine = String(line.dropFirst(6))
            } else if line.isEmpty, !dataLine.isEmpty {
                if let data = dataLine.data(using: .utf8), let event = try? JSONDecoder().decode(SessionStreamEvent.self, from: data) {
                    await onEvent(event)
                }
                dataLine = ""
            }
        }
        if let data = dataLine.data(using: .utf8), let event = try? JSONDecoder().decode(SessionStreamEvent.self, from: data) {
            await onEvent(event)
        }
        if !Task.isCancelled { throw AgenthailAPIError.streamClosed }
    }

    func sessionStreamBody(id: String, ref: String, start: Int = 0, end: Int? = nil) async throws -> SessionStreamBody {
        var components = URLComponents()
        components.path = "/api/v1/session-stream-body"
        var query = [URLQueryItem(name: "id", value: id), URLQueryItem(name: "ref", value: ref), URLQueryItem(name: "start", value: String(start))]
        if let end { query.append(URLQueryItem(name: "end", value: String(end))) }
        components.queryItems = query
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        return try await get(path)
    }

    func streamCatalog(after: UInt64, onConnected: @escaping @Sendable () async -> Void, onEvent: @escaping @Sendable (CatalogStreamEvent) async -> Void) async throws {
        var components = URLComponents()
        components.path = "/api/v1/catalog-events"
        components.queryItems = [URLQueryItem(name: "after", value: String(after))]
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        var request = authorizedRequest(path: path)
        request.setValue(String(after), forHTTPHeaderField: "Last-Event-ID")
        let (bytes, response) = try await session.bytes(for: request)
        if let response = response as? HTTPURLResponse, response.statusCode == 409 {
            throw AgenthailAPIError.streamGap
        }
        try validate(response: response, data: nil)
        await onConnected()
        var dataLine = ""
        for try await line in bytes.lines {
            if Task.isCancelled { return }
            if line.hasPrefix("data: ") {
                dataLine = String(line.dropFirst(6))
            } else if line.isEmpty, !dataLine.isEmpty {
                if let data = dataLine.data(using: .utf8), let event = try? JSONDecoder().decode(CatalogStreamEvent.self, from: data) {
                    await onEvent(event)
                }
                dataLine = ""
            }
        }
        if let data = dataLine.data(using: .utf8), let event = try? JSONDecoder().decode(CatalogStreamEvent.self, from: data) {
            await onEvent(event)
        }
        if !Task.isCancelled { throw AgenthailAPIError.streamClosed }
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        try await request(path, method: "GET", body: nil)
    }

    private func post<T: Decodable>(_ path: String, body: [String: Any]) async throws -> T {
        try await request(path, method: "POST", body: body)
    }

    private func requestEncoded<T: Decodable, Body: Encodable>(_ path: String, method: String, body: Body, timeout: TimeInterval = 25, idempotencyKey: String? = nil) async throws -> T {
        var request = authorizedRequest(path: path)
        request.httpMethod = method
        request.timeoutInterval = timeout
        request.httpBody = try JSONEncoder().encode(body)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        setIdempotencyHeader(on: &request, path: path, method: method, key: idempotencyKey)
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func request<T: Decodable>(_ path: String, method: String, body: [String: Any]?, timeout: TimeInterval = 25, idempotencyKey: String? = nil) async throws -> T {
        var request = authorizedRequest(path: path)
        request.httpMethod = method
        request.timeoutInterval = timeout
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        setIdempotencyHeader(on: &request, path: path, method: method, key: idempotencyKey)
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

    private func setIdempotencyHeader(on request: inout URLRequest, path: String, method: String, key: String?) {
        guard method == "POST", path == "/api/v1/actions" else { return }
        request.setValue(key ?? UUID().uuidString, forHTTPHeaderField: "Idempotency-Key")
    }

    private func validate(response: URLResponse, data: Data?) throws {
        guard let response = response as? HTTPURLResponse else { throw AgenthailAPIError.invalidResponse }
        guard (200..<300).contains(response.statusCode) else {
            var message = HTTPURLResponse.localizedString(forStatusCode: response.statusCode)
            if let data, let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                if let error = object["error"] as? [String: String], let detail = error["message"] {
                    message = detail
                }
                if let error = object["error"] as? [String: Any], error["code"] as? String == "history_gap" {
                    throw AgenthailAPIError.historyGap("The oldest activity is no longer retained. Current activity is still available here; no new session is needed.")
                }
            }
            throw AgenthailAPIError.request(response.statusCode, message)
        }
    }

}

private struct InstructionRequest: Encodable {
    let action: String
    let sessionID: String
    let message: String
    let turnSettings: TurnSettings

    enum CodingKeys: String, CodingKey { case action; case sessionID = "sessionId"; case message; case effort; case mode }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(action, forKey: .action)
        try container.encode(sessionID, forKey: .sessionID)
        try container.encode(message, forKey: .message)
        if action != "steer" {
            try container.encodeIfPresent(turnSettings.effort, forKey: .effort)
            try container.encodeIfPresent(turnSettings.mode, forKey: .mode)
        }
    }
}

private struct SessionCreateRequest: Encodable {
    let action: String
    let surface: String
    let message: String
    let cwd: String
    let model: String
    let turnSettings: TurnSettings
    let launcher: String?

    enum CodingKeys: String, CodingKey { case action; case surface; case message; case cwd; case model; case effort; case mode; case launcher }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(action, forKey: .action)
        try container.encode(surface, forKey: .surface)
        try container.encode(message, forKey: .message)
        try container.encode(cwd, forKey: .cwd)
        try container.encode(model, forKey: .model)
        try container.encodeIfPresent(turnSettings.effort, forKey: .effort)
        try container.encodeIfPresent(turnSettings.mode, forKey: .mode)
        try container.encodeIfPresent(launcher, forKey: .launcher)
    }
}

private struct EmptyResponse: Decodable {}

struct ActionReceipt: Decodable {
    let result: DeliveryReceipt?
}
struct DeliveryReceipt: Decodable {
    let deliveryId: Int64?
    let evidence: String?
    let status: String?
    let queueId: Int64?
    let detail: String?
}
