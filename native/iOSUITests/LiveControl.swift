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

    private func post(_ path: String) throws -> Data {
        var request = URLRequest(url: base.appendingPathComponent(path), timeoutInterval: 30)
        request.httpMethod = "POST"
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
