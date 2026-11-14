import Foundation

struct ExtensionAppOffer: Codable, Equatable, Sendable {
    var label: String?
    var icon: String?
    var order: Int?
}

struct ExtensionMount: Codable, Equatable, Sendable {
    let name: String
    let label: String
    var port: Int?
    var entry: String
    var state: String
    var error: String?
    var published: Bool?
    var app: ExtensionAppOffer?
    var badge: String?
}

struct ExtensionPage: Codable, Equatable, Sendable {
    var port: Int?
    var entry: String
    var published: Bool?
    var error: String?
}

struct ExtensionSummary: Codable, Equatable, Sendable, Identifiable {
    let id: String
    let label: String
    var description: String?
    var state: String
    var error: String?
    var page: ExtensionPage?
    var app: ExtensionAppOffer?
    var badge: String?
    var mounts: [ExtensionMount]?
}

struct ExtensionsList: Codable, Equatable, Sendable {
    var host: String
    var hostError: String?
    var extensions: [ExtensionSummary]
}

struct ExtensionWebSession: Codable, Equatable, Sendable {
    let name: String
    let value: String
    let path: String
    let expiresAt: Date
    let scopes: [String]
}

struct ExtensionChange: Codable, Equatable, Sendable, Identifiable {
    struct Files: Codable, Equatable, Sendable {
        let id: String
        var created: Bool?
        var removed: Bool?
        var added: [String]?
        var updated: [String]?
        var deleted: [String]?
    }

    let rev: String
    var author: String?
    let time: Date
    var message: String?
    var extensions: [Files]?
    var root: Bool?
    var pending: Bool?

    var id: String { rev }

    func touches(_ extensionID: String) -> Bool {
        extensions?.contains { $0.id == extensionID } ?? false
    }
}

struct ExtensionChanges: Codable, Sendable {
    let changes: [ExtensionChange]
}

struct ExtensionBuildStarted: Codable, Sendable {
    struct Session: Codable, Sendable { let id: String }
    var session: Session?
}

enum ExtensionDates {
    static func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            if let date = fractional.date(from: text) ?? plain.date(from: text) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "not an RFC 3339 time: \(text)"))
        }
        return decoder
    }

    nonisolated(unsafe) private static let fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    nonisolated(unsafe) private static let plain = ISO8601DateFormatter()
}
