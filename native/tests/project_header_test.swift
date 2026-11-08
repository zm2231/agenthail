@testable import AgenthailHelpers
import Foundation
import Network

@main
@MainActor
struct ProjectHeaderTest {
    static func main() async throws {
        let local = ProjectState.Lead(host: SessionHost(id: "local", name: "mac", state: "local"), name: "lead", surface: "claude", sessionId: "s1")
        let remote = ProjectState.Lead(host: SessionHost(id: "h2", name: "forge.tail.net", state: "connected"), name: "lead", surface: "codex", sessionId: "s9")
        let unstarted = ProjectState.Lead(name: "lead", surface: "claude")

        check(ProjectHeaderFacts.leadChip([]) == nil, "a project without a lead has no chip")
        check(ProjectHeaderFacts.leadChip([local]) == .open(label: "@lead", help: "Open @lead, the project coordinator", sessionID: "s1"), "a running lead opens its session")
        check(ProjectHeaderFacts.leadChip([remote]) == .open(label: "forge::@lead", help: "Open forge::@lead, the project coordinator", sessionID: "h2::s9"), "a lead on another host is named and opened there")
        check(ProjectHeaderFacts.leadChip([unstarted]) == .waiting(label: "@lead", help: "@lead starts on its first message"), "a lead without a session says it starts on its first message")
        check(ProjectHeaderFacts.leadChip([local, remote]) == .conflict(label: "2 leads", help: "Several hosts each have a coordinator: @lead, forge::@lead. Change the lead to keep one."), "several leads warn and name each")

        let result = try JSONDecoder().decode(ChannelSendResult.self, from: Data(##"{"ok":true,"target":"#p1","recipients":[{"coordinator":"@lead","outcome":"sent"},{"sessionId":"s2","outcome":"queued"},{"sessionId":"s3","coordinator":"","outcome":"submitted"}],"sent":1,"queued":1,"submitted":1,"failed":0}"##.utf8))
        check(ProjectHeaderFacts.sent(result) { $0 == "s2" ? "tests" : $0 } == "Sent to 3: @lead, tests, s3.", "the notice names each recipient: \(ProjectHeaderFacts.sent(result) { $0 })")

        let none = try JSONDecoder().decode(ChannelSendResult.self, from: Data(#"{"recipients":[{"sessionId":"s4","outcome":"failed"}]}"#.utf8))
        check(ProjectHeaderFacts.sent(none) { $0 } == "Sent to 0. Not delivered to s4.", "a broadcast that reached no one says so: \(ProjectHeaderFacts.sent(none) { $0 })")

        try await broadcasts()
        print("ok")
    }

    static func broadcasts() async throws {
        var requests: [String] = []
        let server = try StubServer { request in
            guard request.hasPrefix("POST /api/v1/actions") else { return reply("200 OK", "{}") }
            requests.append(request)
            if request.contains("\"message\":\"partial\"") {
                return reply("409 Conflict", ##"{"error":{"code":"conflict","message":"channel delivery: 1 sent, 0 queued, 0 submitted, 1 failed (@lead sent; s2 failed: host unreachable)"},"target":"#p1","recipients":[{"coordinator":"@lead","outcome":"sent"},{"sessionId":"s2","outcome":"failed","detail":"host unreachable"}],"sent":1,"queued":0,"submitted":0,"failed":1}"##)
            }
            if request.contains("\"message\":\"busy\"") {
                return reply("409 Conflict", #"{"error":{"code":"conflict","message":"another send is in progress"}}"#)
            }
            if request.contains("\"message\":\"refuse\"") {
                return reply("400 Bad Request", #"{"error":{"code":"invalid_request","message":"no active conversations in #p1"}}"#)
            }
            return reply("200 OK", #"{"ok":true,"recipients":[{"coordinator":"@lead","outcome":"sent"},{"sessionId":"s2","outcome":"queued"}],"sent":1,"queued":1,"submitted":0,"failed":0}"#)
        }
        let port = await server.ready()
        let model = AgenthailModel(connecting: false, api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(port)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        let project = ProjectState(id: "p1", displayName: "Agenthail")

        let notice = try await model.broadcast(to: project, message: "ship it", idempotencyKey: "k1")
        let posted = requests.first ?? ""
        check(notice == "Sent to 2: @lead, s2.", "a broadcast reports who got it: \(notice)")
        check(posted.contains(#""action":"channel-send""#) && posted.contains(##""channel":"#p1""##) && posted.contains(#""author":"person""#) && posted.contains(#""message":"ship it""#), "it sends to the project's channel as the person: \(posted)")
        check(posted.lowercased().contains("idempotency-key: k1"), "the send carries its idempotency key")

        let partial = try await model.broadcast(to: project, message: "partial", idempotencyKey: "k3")
        check(partial == "Sent to 1: @lead. Not delivered to s2: host unreachable.", "a partly delivered broadcast says who got it and who didn't: \(partial)")

        do {
            _ = try await model.broadcast(to: project, message: "busy", idempotencyKey: "k4")
            check(false, "a conflict without recipients throws")
        } catch {
            check(AgenthailAPIError.userMessage(error).contains("another send is in progress"), "a conflict without recipients says why: \(AgenthailAPIError.userMessage(error))")
        }

        do {
            _ = try await model.broadcast(to: project, message: "refuse", idempotencyKey: "k2")
            check(false, "a refused broadcast throws")
        } catch {
            check(AgenthailAPIError.userMessage(error).contains("no active conversations"), "a refused broadcast says why: \(AgenthailAPIError.userMessage(error))")
        }
        server.stop()
    }

    static func reply(_ status: String, _ body: String) -> String {
        "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
    }

    final class StubServer: @unchecked Sendable {
        private let listener: NWListener
        private var connections: [NWConnection] = []

        init(respond: @escaping (String) -> String) throws {
            listener = try NWListener(using: .tcp, on: .any)
            listener.newConnectionHandler = { [self] connection in
                connections.append(connection)
                connection.start(queue: .main)
                read(connection, Data()) { request in
                    connection.send(content: Data(respond(request).utf8), completion: .contentProcessed { _ in })
                }
            }
        }

        private func read(_ connection: NWConnection, _ received: Data, then handle: @escaping (String) -> Void) {
            connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [self] data, _, complete, error in
                let all = received + (data ?? Data())
                let text = String(decoding: all, as: UTF8.self)
                if let end = text.range(of: "\r\n\r\n") {
                    let length = text[..<end.lowerBound].components(separatedBy: "\r\n")
                        .first { $0.lowercased().hasPrefix("content-length:") }
                        .flatMap { Int($0.split(separator: ":")[1].trimmingCharacters(in: .whitespaces)) } ?? 0
                    if text[end.upperBound...].utf8.count >= length || complete || error != nil { return handle(text) }
                } else if complete || error != nil {
                    return handle(text)
                }
                read(connection, all, then: handle)
            }
        }

        func ready() async -> UInt16 {
            await withCheckedContinuation { continuation in
                listener.stateUpdateHandler = { [listener] state in
                    if case .ready = state { continuation.resume(returning: listener.port!.rawValue) }
                }
                listener.start(queue: .main)
            }
        }

        func stop() {
            connections.forEach { $0.cancel() }
            listener.cancel()
        }
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
