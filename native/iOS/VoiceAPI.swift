import Foundation

struct VoiceState: Decodable {
    let `protocol`: Int
    let session: RawSession?
    let phase: String
    let attemptId: String?
    let skillDigest: String?
    let message: String?
    let sdp: String?
    let events: [VoiceEvent]
    let truncated: Bool
    let occupied: Bool
    let textReceipt: String?
    let speechReceipt: String?

    var hasCall: Bool { ["starting", "negotiating", "connected", "stopping", "unknown"].contains(phase) }
    var transcripts: [VoiceTranscript] {
        var result: [VoiceTranscript] = []
        var pending: [String: Int] = [:]
        for event in events {
            if event.method == "thread/realtime/started" { pending = [:] }
            guard let role = event.params.role, ["user", "assistant"].contains(role) else { continue }
            switch event.method {
            case "thread/realtime/transcript/delta":
                guard let delta = event.params.delta else { continue }
                if let index = pending[role] { result[index].text += delta }
                else {
                    pending[role] = result.count
                    result.append(VoiceTranscript(id: String(event.sequence), role: role, text: delta))
                }
            case "thread/realtime/transcript/done":
                guard let text = event.params.text else { continue }
                if let index = pending.removeValue(forKey: role) { result[index].text = text }
                else { result.append(VoiceTranscript(id: String(event.sequence), role: role, text: text)) }
            default: break
            }
        }
        return result
    }
}

struct VoiceEvent: Decodable {
    let sequence: Int64
    let method: String
    let params: Params
    struct Params: Decodable {
        let delta: String?
        let role: String?
        let text: String?
        let reason: String?
    }
}

struct VoiceTranscript: Identifiable {
    let id: String
    let role: String
    var text: String
}

struct VoiceAction: Encodable {
    var action: String
    var attemptId: String? = nil
    var sdp: String? = nil
    var text: String? = nil
    var messageId: String? = nil
}

@MainActor
protocol VoiceServiceClient {
    func request(path: String) -> URLRequest
    func state() async throws -> VoiceState
    func action(_ action: VoiceAction) async throws -> VoiceState
}

@MainActor
final class VoiceAPI: VoiceServiceClient {
    let endpoint: URL
    private let token: String
    private let session: URLSession

    init(endpoint: URL, token: String, session: URLSession = URLSession(configuration: .ephemeral, delegate: VoiceRedirectPolicy(), delegateQueue: nil)) throws {
        guard endpoint.scheme == "https", endpoint.host != nil, endpoint.user == nil,
              endpoint.password == nil, endpoint.query == nil, endpoint.fragment == nil,
              endpoint.path.isEmpty || endpoint.path == "/" else {
            throw AgenthailAPIError.unavailable("Voice requires the paired Mac's secure HTTPS address.")
        }
        self.endpoint = endpoint
        self.token = token
        self.session = session
    }

    func request(path: String) -> URLRequest {
        var request = URLRequest(url: endpoint.appendingPathComponent(path))
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.cachePolicy = .reloadIgnoringLocalCacheData
        request.timeoutInterval = 30
        return request
    }

    func state() async throws -> VoiceState { try await perform(nil) }
    func action(_ action: VoiceAction) async throws -> VoiceState { try await perform(action) }

    private func perform(_ action: VoiceAction?) async throws -> VoiceState {
        var request = request(path: "api/v1/voice")
        if let action {
            request.httpMethod = "POST"
            request.httpBody = try JSONEncoder().encode(action)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw AgenthailAPIError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            struct Failure: Decodable { let error: Detail; struct Detail: Decodable { let message: String } }
            let failure = try? JSONDecoder().decode(Failure.self, from: data)
            throw AgenthailAPIError.request(http.statusCode, failure?.error.message ?? "Voice connection failed (\(http.statusCode)).")
        }
        let state = try JSONDecoder().decode(VoiceState.self, from: data)
        guard state.protocol == 1 else { throw AgenthailAPIError.incompatible(state.protocol) }
        return state
    }
}

private final class VoiceRedirectPolicy: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
