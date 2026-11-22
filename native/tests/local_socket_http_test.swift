@testable import AgenthailHelpers
import Darwin
import Foundation
import Network

@main
@MainActor
struct LocalSocketHTTPTest {
    static let view = #"{"roles":[{"name":"worker","builtIn":true,"edited":false,"description":"d","reach":"descendants","decisions":{"project-archive":"ask"},"definition":{"reach":"descendants","tools":null},"tools":["agenthail_send"],"allTools":true,"depth":3,"running":8}],"assignments":[],"actions":[{"name":"project-archive","default":"notify","detail":"Archive","ownProject":true}],"tools":["agenthail_send"]}"#

    static func main() async throws {
        let path = "/tmp/ah-lsh-\(getpid()).sock"
        defer { unlink(path) }

        var requests: [String] = []
        let server = try UnixServer(path: path) { request in
            requests.append(request)
            if request.contains("\"op\":\"assign\"") {
                return reply("403 Forbidden", #"{"error":{"code":"person_only","message":"Agent roles are the person's to change, and this command runs inside session s1."}}"#)
            }
            if request.contains("\"op\":\"hang\"") { return nil }
            if request.hasPrefix("POST") { return chunked(view) }
            return reply("200 OK", view)
        }
        let socket = LocalSocketHTTP(path: path, timeout: 1)

        let read: AgentRolesView = try await socket.json("GET", "/api/v1/agents")
        check(read.roles.first?.name == "worker" && read.roles.first?.definition.tools == nil, "a read decodes the roles view")
        let get = requests.last ?? ""
        check(get.hasPrefix("GET /api/v1/agents HTTP/1.1\r\n") && get.contains("\r\nConnection: close\r\n") && get.contains("\r\nHost: localhost\r\n") && !get.lowercased().contains("authorization"), "a read is HTTP/1.1 with Connection: close and no token: \(get)")

        let body = try JSONEncoder().encode(AgentRoleRequest.put("reviewer", AgentRoleDefinition(reach: "project", actions: ["project-archive": "ask"])))
        let written: AgentRolesView = try await socket.json("POST", "/api/v1/agents", body: body)
        check(written.roles.count == 1, "a chunked answer decodes")
        let post = requests.last ?? ""
        check(post.contains("Content-Type: application/json") && post.contains("Content-Length: \(body.count)") && post.hasSuffix(String(decoding: body, as: UTF8.self)), "a write sends its JSON body: \(post)")
        check(post.contains("\"tools\":null"), "a put sends tools null for every tool: \(post)")

        do {
            let _: AgentRolesView = try await socket.json("POST", "/api/v1/agents", body: JSONEncoder().encode(AgentRoleRequest.assign("lead", kind: .session, target: "s1")))
            check(false, "a refused write throws")
        } catch let error as LocalSocketError {
            check(error == .refused(403, "Agent roles are the person's to change, and this command runs inside session s1.") && error.localizedDescription.hasPrefix("Agent roles are the person's"), "a refusal shows the daemon's own words: \(error)")
        }

        let started = Date()
        do {
            let _: AgentRolesView = try await socket.json("POST", "/api/v1/agents", body: Data(#"{"op":"hang"}"#.utf8))
            check(false, "a stuck daemon times out")
        } catch let error as LocalSocketError {
            check(error == .timedOut(1) && Date().timeIntervalSince(started) < 3, "a stuck daemon ends in the timeout: \(error)")
            check(error.localizedDescription.contains("did not answer within 1 seconds"), "the timeout says so")
        }
        server.stop()

        do {
            _ = try await LocalSocketHTTP(path: path + ".missing").send("GET", "/api/v1/agents")
            check(false, "a missing socket throws")
        } catch let error as LocalSocketError {
            check(error == .noSocket, "a missing socket is an older daemon: \(error)")
        }

        let stale = "/tmp/ah-lsh-stale-\(getpid()).sock"
        defer { unlink(stale) }
        let fd = UnixServer.bound(stale)
        close(fd)
        do {
            _ = try await LocalSocketHTTP(path: stale).send("GET", "/api/v1/agents")
            check(false, "a socket nobody listens on throws")
        } catch let error as LocalSocketError {
            check(error == .noSocket, "a socket nobody listens on is an older daemon: \(error)")
        }

        try await transportFallsBack(missing: path + ".missing")
        print("ok")
    }

    static func transportFallsBack(missing: String) async throws {
        let tcp = try TCPStub(body: view)
        let port = await tcp.ready()
        let api = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(port)")!, token: "t", session: URLSession(configuration: .ephemeral))
        let transport = MacAgentRolesTransport(socket: LocalSocketHTTP(path: missing), api: api)
        let store = AgentRolesStore()
        store.attach(transport)
        await store.load()
        check(store.view?.roles.count == 1 && store.readOnlyReason == MacAgentRolesTransport.readOnlyReason && !store.editable, "without the socket the roles read over the dashboard API, read-only")
        check(MacAgentRolesTransport.readOnlyReason.hasPrefix("Update the Agenthail daemon to change roles"), "read-only says to update the daemon")
        let sent = await store.send(.delete("reviewer"))
        check(!sent && store.saveError == MacAgentRolesTransport.readOnlyReason, "a write without the socket says to update the daemon")
        tcp.stop()

        let silent = try TCPStub(body: nil)
        let silentPort = await silent.ready()
        let quiet = AgentRolesAPITransport(api: AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:\(silentPort)")!, token: "t", session: URLSession(configuration: .ephemeral)))
        for write in [false, true] {
            let started = Date()
            do {
                if write { _ = try await quiet.write(.delete("reviewer")) } else { _ = try await quiet.read() }
                check(false, "a silent computer times out")
            } catch let error as AgentRolesTimeout {
                let took = Date().timeIntervalSince(started)
                check(error.seconds == AgentRolesAPITransport.timeout && took < Double(AgentRolesAPITransport.timeout) + 3 && error.localizedDescription.contains("did not answer within 5 seconds"), "a silent computer ends in a clear timeout within a few seconds (write: \(write), took \(took))")
            }
        }
        silent.stop()
    }

    nonisolated static func reply(_ status: String, _ body: String) -> String {
        "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
    }

    nonisolated static func chunked(_ body: String) -> String {
        let bytes = Array(body.utf8)
        let half = bytes.count / 2
        let parts = [Array(bytes[..<half]), Array(bytes[half...])]
        var out = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n"
        for part in parts { out += String(part.count, radix: 16) + "\r\n" + String(decoding: part, as: UTF8.self) + "\r\n" }
        return out + "0\r\n\r\n"
    }

    final class UnixServer: @unchecked Sendable {
        private let fd: Int32
        private var held: [Int32] = []
        private var stopped = false

        static func bound(_ path: String) -> Int32 {
            unlink(path)
            let fd = socket(AF_UNIX, SOCK_STREAM, 0)
            var address = sockaddr_un()
            address.sun_family = sa_family_t(AF_UNIX)
            withUnsafeMutableBytes(of: &address.sun_path) { raw in
                path.utf8CString.withUnsafeBytes { raw.copyMemory(from: UnsafeRawBufferPointer(rebasing: $0.prefix(raw.count))) }
            }
            let size = socklen_t(MemoryLayout<sockaddr_un>.size)
            let result = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, size) } }
            precondition(result == 0, "bind \(path): \(errno)")
            return fd
        }

        init(path: String, respond: @escaping @MainActor (String) -> String?) throws {
            fd = Self.bound(path)
            precondition(listen(fd, 8) == 0)
            let fd = fd
            Thread.detachNewThread { [self] in
                while true {
                    let client = accept(fd, nil, nil)
                    if client < 0 { return }
                    let request = Self.read(client)
                    let answer = DispatchQueue.main.sync { MainActor.assumeIsolated { respond(request) } }
                    if let answer {
                        _ = answer.utf8CString.withUnsafeBufferPointer { write(client, $0.baseAddress, $0.count - 1) }
                        close(client)
                    } else {
                        DispatchQueue.main.sync { held.append(client) }
                    }
                }
            }
        }

        private static func read(_ client: Int32) -> String {
            var data = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while true {
                let count = Darwin.read(client, &buffer, buffer.count)
                if count <= 0 { break }
                data.append(buffer, count: count)
                let text = String(decoding: data, as: UTF8.self)
                if let end = text.range(of: "\r\n\r\n") {
                    let length = text[..<end.lowerBound].components(separatedBy: "\r\n")
                        .first { $0.lowercased().hasPrefix("content-length:") }
                        .flatMap { Int($0.split(separator: ":")[1].trimmingCharacters(in: .whitespaces)) } ?? 0
                    if text[end.upperBound...].utf8.count >= length { break }
                }
            }
            return String(decoding: data, as: UTF8.self)
        }

        func stop() {
            held.forEach { close($0) }
            shutdown(fd, SHUT_RDWR)
            close(fd)
        }
    }

    final class TCPStub: @unchecked Sendable {
        private let listener: NWListener
        private var connections: [NWConnection] = []

        init(body: String?) throws {
            listener = try NWListener(using: .tcp, on: .any)
            listener.newConnectionHandler = { [self] connection in
                connections.append(connection)
                connection.start(queue: .main)
                connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { _, _, _, _ in
                    guard let body else { return }
                    connection.send(content: Data(LocalSocketHTTPTest.reply("200 OK", body).utf8), completion: .contentProcessed { _ in })
                }
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
