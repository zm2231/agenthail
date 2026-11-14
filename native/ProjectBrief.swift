import Foundation
import Observation

struct ProjectBriefReply: Decodable {
    struct Since: Decodable {
        let at: String
        var fallback: Bool? = nil
    }

    var ref: String? = nil
    var result: DeliveryReceipt? = nil
    var started: Bool? = nil
    var since: Since? = nil
}

enum ProjectBrief {
    static let title = "Catch me up"

    static func path(projectID: String) -> String {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "/?#%;")
        return "/api/v1/projects/" + (projectID.addingPercentEncoding(withAllowedCharacters: allowed) ?? projectID) + "/brief"
    }

    static func notice(_ reply: ProjectBriefReply, lead: String, timeZone: TimeZone = .current, locale: Locale = .current) -> String {
        let name = reply.ref.flatMap { $0.isEmpty ? nil : $0 } ?? "@" + lead
        var notice = "Asked \(name) to catch you up."
        if let since = reply.since {
            if since.fallback == true {
                notice = "Asked \(name) to catch you up on the last 24 hours."
            } else if let at = TimestampParser.date(since.at) {
                var style = Date.FormatStyle(date: .abbreviated, time: .shortened, locale: locale)
                style.timeZone = timeZone
                notice = "Asked \(name) to catch you up since \(at.formatted(style))."
            }
        }
        if reply.started == true {
            notice += " This starts its conversation."
        } else if reply.result?.evidence == "queued" {
            notice += " It replies after its current turn."
        }
        return notice
    }
}

extension AgenthailAPI {
    func projectBrief(projectID: String, idempotencyKey: String) async throws -> ProjectBriefReply {
        var request = authorizedRequest(path: ProjectBrief.path(projectID: projectID))
        request.httpMethod = "POST"
        request.setValue(idempotencyKey, forHTTPHeaderField: "Idempotency-Key")
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try JSONDecoder().decode(ProjectBriefReply.self, from: data)
    }
}

@MainActor
@Observable
final class ProjectBriefAsk {
    private(set) var asking = false
    private(set) var notice: String?
    private(set) var failure: String?
    private var key = UUID().uuidString

    func ask(_ run: (String) async throws -> String) async {
        guard !asking else { return }
        asking = true
        notice = nil
        failure = nil
        defer { asking = false }
        do {
            notice = try await run(key)
            key = UUID().uuidString
        } catch {
            failure = AgenthailAPIError.userMessage(error)
        }
    }
}
