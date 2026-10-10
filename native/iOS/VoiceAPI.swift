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
    let target: RawSession?
    let audioProvider: String?
    let codingProvider: String?
    let inputMuted: Bool?
    let inputRevision: UInt64?
    let contextFrames: [VoiceContextFrame]?
    let bindingGeneration: UInt64?

    var hasCall: Bool { ["starting", "negotiating", "connected", "stopping", "unknown"].contains(phase) }
    var transcripts: [VoiceTranscript] {
        if events.contains(where: { $0.params.item?.type == "transcriptSegment" && $0.params.item?.realtimeSessionId == attemptId }) {
            var result: [VoiceTranscript] = []
            var indices: [String: Int] = [:]
            for event in events {
                if let item = event.params.item, item.type == "transcriptSegment",
                   item.realtimeSessionId == attemptId, ["user", "assistant"].contains(item.role ?? ""),
                   ["thread/realtime/item/started", "thread/realtime/item/completed"].contains(event.method) {
                    if let index = indices[item.id] { result[index].text = item.text ?? ""; result[index].inputMuted = result[index].inputMuted || event.params.inputMuted == true }
                    else {
                        indices[item.id] = result.count
                        result.append(VoiceTranscript(id: item.id, role: item.role!, text: item.text ?? "", inputMuted: event.params.inputMuted == true))
                    }
                } else if event.method == "thread/realtime/item/transcript/delta",
                          let id = event.params.itemId, let index = indices[id], let delta = event.params.delta {
                    result[index].text += delta
                }
            }
            return result.filter { !$0.text.isEmpty }
        }
        var result: [VoiceTranscript] = []
        var pending: [String: Int] = [:]
        for event in events {
            if event.method == "thread/realtime/started" { pending = [:] }
            guard let role = event.params.role, ["user", "assistant"].contains(role) else { continue }
            switch event.method {
            case "thread/realtime/transcript/delta":
                guard let delta = event.params.delta else { continue }
                if let index = pending[role] { result[index].text += delta; result[index].inputMuted = result[index].inputMuted || event.params.inputMuted == true }
                else {
                    pending[role] = result.count
                    result.append(VoiceTranscript(id: String(event.sequence), role: role, text: delta, inputMuted: event.params.inputMuted == true))
                }
            case "thread/realtime/transcript/done":
                guard let text = event.params.text else { continue }
                if let index = pending.removeValue(forKey: role) { result[index].text = text; result[index].inputMuted = result[index].inputMuted || event.params.inputMuted == true }
                else { result.append(VoiceTranscript(id: String(event.sequence), role: role, text: text, inputMuted: event.params.inputMuted == true)) }
            default: break
            }
        }
        return result
    }
}

struct VoiceContextFrame: Decodable {
    let id: UInt64
    let generation: UInt64
    let text: String
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
        let itemId: String?
        let item: Item?
        let inputMuted: Bool?
        private enum CodingKeys: String, CodingKey { case delta, role, text, reason, itemId, item, inputMuted }
        init(from decoder: any Decoder) throws {
            let values = try decoder.container(keyedBy: CodingKeys.self)
            delta = try values.decodeIfPresent(String.self, forKey: .delta)
            role = try values.decodeIfPresent(String.self, forKey: .role)
            text = try values.decodeIfPresent(String.self, forKey: .text)
            reason = try values.decodeIfPresent(String.self, forKey: .reason)
            itemId = try values.decodeIfPresent(String.self, forKey: .itemId)
            inputMuted = try values.decodeIfPresent(Bool.self, forKey: .inputMuted)
            // itemAdded also carries arbitrary JSON, not a canonical transcript item.
            item = try? values.decode(Item.self, forKey: .item)
        }
    }
    struct Item: Decodable {
        let id: String
        let realtimeSessionId: String
        let type: String
        let role: String?
        let text: String?
    }
}

struct VoiceTranscript: Identifiable {
    let id: String
    let role: String
    var text: String
    var inputMuted = false
    var label: String { role == "user" ? (inputMuted ? "Provider transcript · mic muted" : "You") : "Assistant" }
}

struct VoiceAction: Encodable {
    var action: String
    var attemptId: String? = nil
    var sdp: String? = nil
    var text: String? = nil
    var messageId: String? = nil
    var targetId: String? = nil
    var heartbeatMode: String? = nil
    var muted: Bool? = nil
    var inputRevision: UInt64? = nil
    var contextAck: UInt64? = nil
    var contextTransport: String? = nil
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
