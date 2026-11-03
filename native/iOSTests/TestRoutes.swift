import Foundation

extension URLProtocol {
    func answeredProjectActivity() -> Bool {
        guard request.url?.path == "/api/v1/projects/activity" else { return false }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(#"{"projects":[]}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
        return true
    }
}
