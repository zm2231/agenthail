import Foundation
import Network

enum LocalSocketError: LocalizedError, Equatable {
    case noSocket
    case timedOut(TimeInterval)
    case refused(Int, String)
    case broken(String)

    var errorDescription: String? {
        switch self {
        case .noSocket: "This Agenthail daemon does not take changes from the app. Update the Agenthail daemon to change roles."
        case .timedOut(let seconds): "Agenthail did not answer within \(Int(seconds)) seconds. Try again, or restart Agenthail from General settings."
        case .refused(_, let message): message
        case .broken(let detail): "Agenthail's answer could not be read: \(detail)"
        }
    }
}

struct LocalSocketResponse: Sendable {
    let status: Int
    let body: Data
}

struct LocalSocketHTTP: Sendable {
    let path: String
    var timeout: TimeInterval = 4

    static var agenthail: LocalSocketHTTP {
        LocalSocketHTTP(path: FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".agenthail/local.sock").path)
    }

    func send(_ method: String, _ target: String, body: Data? = nil) async throws -> LocalSocketResponse {
        guard FileManager.default.fileExists(atPath: path) else { throw LocalSocketError.noSocket }
        var head = "\(method) \(target) HTTP/1.1\r\nHost: localhost\r\nAccept: application/json\r\nConnection: close\r\n"
        if let body { head += "Content-Type: application/json\r\nContent-Length: \(body.count)\r\n" }
        let request = Data((head + "\r\n").utf8) + (body ?? Data())
        let raw = try await exchange(request)
        return try Self.parse(raw)
    }

    func json<T: Decodable>(_ method: String, _ target: String, body: Data? = nil, as type: T.Type = T.self) async throws -> T {
        let response = try await send(method, target, body: body)
        guard (200..<300).contains(response.status) else {
            throw LocalSocketError.refused(response.status, Self.message(response))
        }
        do {
            return try JSONDecoder().decode(T.self, from: response.body)
        } catch {
            throw LocalSocketError.broken(error.localizedDescription)
        }
    }

    static func message(_ response: LocalSocketResponse) -> String {
        if let object = try? JSONSerialization.jsonObject(with: response.body) as? [String: Any],
           let message = (object["error"] as? [String: Any])?["message"] as? String, !message.isEmpty {
            return message
        }
        return HTTPURLResponse.localizedString(forStatusCode: response.status).capitalized
    }

    private func exchange(_ request: Data) async throws -> Data {
        let connection = NWConnection(to: .unix(path: path), using: .tcp)
        let queue = DispatchQueue(label: "agenthail.local-socket")
        let once = Once()
        let connected = Once()
        let timeout = timeout
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Data, Error>) in
                let finish: @Sendable (Result<Data, Error>) -> Void = { result in
                    guard once.claim() else { return }
                    connection.cancel()
                    continuation.resume(with: result)
                }
                queue.asyncAfter(deadline: .now() + timeout) { finish(.failure(LocalSocketError.timedOut(timeout))) }
                connection.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        _ = connected.claim()
                        connection.send(content: request, completion: .contentProcessed { error in
                            if let error { finish(.failure(LocalSocketError.broken(error.localizedDescription))) }
                        })
                        Self.receive(connection, Data(), finish)
                    case .waiting(let error), .failed(let error):
                        if connected.claim() { finish(.failure(Self.connectFailure(error))) }
                    case .cancelled:
                        finish(.failure(CancellationError()))
                    default:
                        break
                    }
                }
                connection.start(queue: queue)
            }
        } onCancel: {
            connection.cancel()
        }
    }

    private static func receive(_ connection: NWConnection, _ received: Data, _ finish: @escaping @Sendable (Result<Data, Error>) -> Void) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { data, _, complete, error in
            let all = received + (data ?? Data())
            if complete || whole(all) {
                finish(.success(all))
            } else if let error {
                finish((try? parse(all)) != nil ? .success(all) : .failure(LocalSocketError.broken(error.localizedDescription)))
            } else {
                receive(connection, all, finish)
            }
        }
    }

    private static func whole(_ raw: Data) -> Bool {
        guard let split = raw.range(of: Data("\r\n\r\n".utf8)) else { return false }
        let head = String(decoding: raw[..<split.lowerBound], as: UTF8.self).lowercased()
        guard head.contains("content-length:") || head.contains("transfer-encoding: chunked") else { return false }
        return (try? parse(raw)) != nil
    }

    private static func connectFailure(_ error: NWError) -> Error {
        if case .posix(let code) = error, [.ENOENT, .ECONNREFUSED, .ENOTSOCK].contains(code) { return LocalSocketError.noSocket }
        return LocalSocketError.broken(error.localizedDescription)
    }

    static func parse(_ raw: Data) throws -> LocalSocketResponse {
        guard let split = raw.range(of: Data("\r\n\r\n".utf8)) else { throw LocalSocketError.broken("no response") }
        let head = String(decoding: raw[..<split.lowerBound], as: UTF8.self).components(separatedBy: "\r\n")
        let statusParts = head.first?.split(separator: " ") ?? []
        guard statusParts.count >= 2, statusParts[0].hasPrefix("HTTP/1."), let status = Int(statusParts[1]) else {
            throw LocalSocketError.broken("no status line")
        }
        var headers: [String: String] = [:]
        for line in head.dropFirst() {
            guard let colon = line.firstIndex(of: ":") else { continue }
            headers[line[..<colon].lowercased()] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        }
        var body = Data(raw[split.upperBound...])
        if headers["transfer-encoding"]?.lowercased().contains("chunked") == true {
            body = try dechunk(body)
        } else if let length = headers["content-length"].flatMap(Int.init) {
            guard body.count >= length else { throw LocalSocketError.broken("the answer was cut short") }
            body = body.prefix(length)
        }
        return LocalSocketResponse(status: status, body: body)
    }

    private static func dechunk(_ data: Data) throws -> Data {
        var out = Data()
        var rest = data[...]
        let crlf = Data("\r\n".utf8)
        while true {
            guard let line = rest.range(of: crlf) else { throw LocalSocketError.broken("the answer was cut short") }
            let sizeText = String(decoding: rest[..<line.lowerBound], as: UTF8.self).split(separator: ";").first ?? ""
            guard let size = Int(sizeText.trimmingCharacters(in: .whitespaces), radix: 16) else { throw LocalSocketError.broken("bad chunk") }
            rest = rest[line.upperBound...]
            if size == 0 { return out }
            guard rest.count >= size + 2 else { throw LocalSocketError.broken("the answer was cut short") }
            out.append(rest.prefix(size))
            rest = rest.dropFirst(size + 2)
        }
    }
}

private final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var claimed = false

    func claim() -> Bool {
        lock.withLock {
            if claimed { return false }
            claimed = true
            return true
        }
    }
}
