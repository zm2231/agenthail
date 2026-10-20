import Foundation
import UserNotifications

enum RequestAnswer {
    static func send(choice: String, text: String? = nil, sessionID: String, requestID: String, api: () throws -> AgenthailAPI, center: UNUserNotificationCenter = .current()) async {
        var failure: String
        do {
            _ = try await api().respond(sessionID: sessionID, requestID: requestID, decision: RequestDecision(choice: choice, message: text))
            return
        } catch AgenthailAPIError.request(409, _) {
            failure = "That request was already answered or has expired."
        } catch {
            failure = "Agenthail could not send your answer: \(error.localizedDescription) Open the session to answer it."
        }
        if let text, !text.isEmpty {
            failure += " You wrote: \(text)"
        }
        let content = UNMutableNotificationContent()
        content.title = "Agenthail"
        content.body = failure
        content.userInfo = ["sessionId": sessionID]
        content.threadIdentifier = sessionID
        try? await center.add(UNNotificationRequest(identifier: "answer-" + requestID, content: content, trigger: nil))
    }
}
