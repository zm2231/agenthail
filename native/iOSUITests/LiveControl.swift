import Foundation
import XCTest

struct LiveControl {
    let base: URL

    init() throws {
        let value = try XCTUnwrap(ProcessInfo.processInfo.environment["AGENTHAIL_LIVE_CONTROL"], "run under cmd/ios-live-harness, which sets AGENTHAIL_LIVE_CONTROL")
        base = try XCTUnwrap(URL(string: value))
    }

    func pairingLink(host: Int) throws -> String {
        let reply = try post("hosts/\(host)/link")
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: reply) as? [String: String])
        return try XCTUnwrap(object["url"], "the link reply has a url: \(object)")
    }

    func stop(host: Int) throws {
        _ = try post("hosts/\(host)/stop")
    }

    func start(host: Int) throws {
        _ = try post("hosts/\(host)/start")
    }

    func reply(host: Int, conversation: Int, text: String) throws {
        _ = try post("hosts/\(host)/reply", body: try JSONSerialization.data(withJSONObject: ["conversation": conversation, "text": text]))
    }

    func startTurn(host: Int, prompt: String) throws {
        _ = try post("hosts/\(host)/turn/start", body: try JSONSerialization.data(withJSONObject: ["text": prompt]))
    }

    func finishTurn(host: Int, reply: String) throws {
        _ = try post("hosts/\(host)/turn/finish", body: try JSONSerialization.data(withJSONObject: ["text": reply]))
    }

    func ask(host: Int, command: String) throws {
        _ = try post("hosts/\(host)/turn/ask", body: try JSONSerialization.data(withJSONObject: ["text": command]))
    }

    func askAnswer(host: Int) throws -> String? {
        let reply = try post("hosts/\(host)/turn/ask", method: "GET")
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: reply) as? [String: Any])
        guard object["answered"] as? Bool == true else { return nil }
        return (object["decision"] as? [String: Any])?["behavior"] as? String
    }

    private func post(_ path: String, body: Data? = nil, method: String = "POST") throws -> Data {
        var request = URLRequest(url: base.appendingPathComponent(path), timeoutInterval: 30)
        request.httpMethod = method
        request.httpBody = body
        let reply = Reply()
        URLSession.shared.dataTask(with: request) { data, response, error in
            if let error {
                reply.result = .failure(error)
            } else if let status = (response as? HTTPURLResponse)?.statusCode, status != 200 {
                reply.result = .failure(URLError(.badServerResponse, userInfo: [NSLocalizedDescriptionKey: "\(path) answered \(status): \(String(decoding: data ?? Data(), as: UTF8.self))"]))
            } else {
                reply.result = .success(data ?? Data())
            }
            reply.done.signal()
        }.resume()
        _ = reply.done.wait(timeout: .now() + 35)
        return try reply.result.get()
    }
}

private final class Reply: @unchecked Sendable {
    let done = DispatchSemaphore(value: 0)
    var result: Result<Data, Error> = .failure(URLError(.timedOut))
}
