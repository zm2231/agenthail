#if DEBUG
import Foundation
import UIKit

enum PreviewStream {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--preview-stream")
    static let follows = enabled || ProcessInfo.processInfo.arguments.contains("--preview-follow")
    static let interval: TimeInterval = 0.6
    static let started = Date()
    nonisolated(unsafe) static var base: UInt64 = 0
    nonisolated(unsafe) static var history = 160

    static var now: Int { history + Int(Date().timeIntervalSince(started) / interval) }

    static func event(_ k: Int, session: String) -> [String: Any] {
        let ts = ISO8601DateFormatter().string(from: started.addingTimeInterval(Double(k - history) * interval))
        let step = k % 6
        let turn = k / 6
        var data: [String: Any] = ["version": k, "op": "upsert", "ts": ts, "truncated": false]
        if k < history, k % 40 == 2 {
            let record = (1...300).map { "{\"call_id\":\"call-\(turn)-\($0)\",\"status\":\"completed\",\"aggregated_output\":\"ok\"}" }.joined(separator: ",\n")
            data.merge(["itemId": "activity-\(turn)", "kind": "event", "title": "Codex activity", "body": record]) { $1 }
            return ["stream": "session", "sessionId": session, "seq": base + UInt64(k), "type": "item", "data": data]
        }
        switch step {
        case 0:
            data.merge(["itemId": "call-\(turn)", "kind": "toolCall", "title": "Bash", "callId": "call-\(turn)", "body": "{\"command\":\"swift test --filter Reconnect\(turn)\"}"]) { $1 }
        case 1:
            data.merge(["itemId": "result-\(turn)", "kind": "toolResult", "title": "Bash result", "callId": "call-\(turn)", "body": (1...80).map { "ok \(turn).\($0) Reconnect\($0).swift" }.joined(separator: "\n")]) { $1 }
        case 2:
            data.merge(["itemId": "thought-\(turn)", "kind": "reasoning", "title": "Thinking", "body": "Checking whether turn \(turn) keeps the queued instruction attached across the reconnect."]) { $1 }
        default:
            let paragraphs = (0...(step - 3)).map { "Paragraph \($0 + 1) of reply \(turn). " + String(repeating: "The pending build survives reconnecting and the queued instruction keeps its identity. ", count: 3 + turn % 4) }
            data.merge(["itemId": "reply-\(turn)", "kind": "message", "role": "assistant", "title": "assistant", "body": paragraphs.joined(separator: "\n\n")]) { $1 }
        }
        return ["stream": "session", "sessionId": session, "seq": base + UInt64(k), "type": "item", "data": data]
    }

    static func items(through k: Int) -> [[String: Any]] {
        var order: [String] = []
        var rows: [String: [String: Any]] = [:]
        for n in stride(from: 1, through: k, by: 1) {
            let data = event(n, session: "")["data"] as! [String: Any]
            let id = data["itemId"] as! String
            if rows[id] == nil { order.append(id) }
            rows[id] = ["id": id, "kind": data["kind"]!, "role": data["role"] ?? NSNull(), "title": data["title"]!, "text": data["body"]!,
                        "callId": data["callId"] ?? NSNull(), "timestamp": data["ts"]!, "truncated": false]
        }
        return order.compactMap { rows[$0] }
    }

    static func detailJSON(base json: String, captured: Bool) -> String {
        var detail = try! JSONSerialization.jsonObject(with: Data(json.utf8)) as! [String: Any]
        if captured, base == 0 {
            base = (detail["journalSeq"] as? NSNumber)?.uint64Value ?? 0
            history = 0
        }
        let k = now
        var timeline = detail["timeline"] as? [String: Any] ?? ["source": "claude", "truncated": false]
        let kept = captured ? timeline["items"] as? [[String: Any]] ?? [] : []
        timeline["items"] = kept + items(through: k)
        if !captured { timeline["nextBefore"] = 0 }
        detail["timeline"] = timeline
        detail["journalSeq"] = base + UInt64(k)
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }

    static func lines(after seq: UInt64, through k: Int, session: String) -> Data {
        let first = Int(seq >= base ? seq - base : 0) + 1
        guard first <= k else { return Data() }
        return (first...k).reduce(into: Data()) { data, n in
            data += Data("id: \(base + UInt64(n))\ndata: ".utf8) + (try! JSONSerialization.data(withJSONObject: event(n, session: session))) + Data("\n\n".utf8)
        }
    }
}

final class PreviewStreamFeed: @unchecked Sendable {
    private var task: Task<Void, Never>?
    private var background: NSObjectProtocol?

    func start(session: String, after: UInt64, send: @escaping @Sendable (Data) -> Void, drop: @escaping @Sendable () -> Void) {
        background = NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: nil) { [weak self] _ in
            self?.stop()
            drop()
        }
        task = Task.detached {
            var sent = after
            while !Task.isCancelled {
                let k = PreviewStream.now
                let data = PreviewStream.lines(after: sent, through: k, session: session)
                if !data.isEmpty {
                    send(data)
                    sent = PreviewStream.base + UInt64(k)
                }
                try? await Task.sleep(for: .milliseconds(100))
            }
        }
    }

    func stop() {
        task?.cancel()
        if let background { NotificationCenter.default.removeObserver(background) }
        background = nil
    }
}
#endif
