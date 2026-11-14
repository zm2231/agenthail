import Foundation

extension AgenthailAPI {
    func extensions(host: String? = nil) async throws -> ExtensionsList {
        try await extensionsRequest(Self.onHost("/api/v1/extensions", host), method: "GET", body: nil)
    }

    func extensionWebSession() async throws -> ExtensionWebSession {
        try await extensionsRequest("/api/v1/web-session", method: "POST", body: nil)
    }

    func extensionChanges(limit: Int = 50, host: String? = nil) async throws -> [ExtensionChange] {
        let changes: ExtensionChanges = try await extensionsRequest(Self.onHost("/api/v1/extensions/history?limit=\(limit)", host), method: "GET", body: nil)
        return changes.changes
    }

    static func onHost(_ path: String, _ host: String?) -> String {
        guard let host, let value = host.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed.subtracting(CharacterSet(charactersIn: "&=+?"))) else { return path }
        return path + (path.contains("?") ? "&" : "?") + "host=" + value
    }

    func undoExtensionChange(_ rev: String, host: String? = nil) async throws {
        let _: ExtensionsUndone = try await extensionsRequest(Self.onHost(Self.undoPath(rev), host), method: "POST", body: nil)
    }

    static func undoPath(_ rev: String) -> String {
        let segment = rev.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/?#"))) ?? rev
        return "/api/v1/extensions/history/\(segment)/undo"
    }

    func buildExtension(_ message: String, host: String? = nil) async throws -> ExtensionBuildStarted {
        try await extensionsRequest(Self.onHost("/api/v1/extensions/build", host), method: "POST", body: ["message": message], idempotencyKey: UUID().uuidString)
    }

    private func extensionsRequest<T: Decodable>(_ path: String, method: String, body: [String: String]?, idempotencyKey: String? = nil) async throws -> T {
        var request = authorizedRequest(path: path)
        request.httpMethod = method
        if let body {
            request.httpBody = try JSONEncoder().encode(body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let idempotencyKey { request.setValue(idempotencyKey, forHTTPHeaderField: "Idempotency-Key") }
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try ExtensionDates.decoder().decode(T.self, from: data)
    }
}

private struct ExtensionsUndone: Decodable {}
