import Foundation
import UserNotifications

enum RequestAnswer {
    static func send(choice: String, text: String? = nil, sessionID: String, requestID: String, api: () throws -> AgenthailAPI, center: UNUserNotificationCenter = .current()) async {
        var failure: String
        do {
            _ = try await api().respond(sessionID: sessionID, requestID: requestID, decision: .choose(choice, message: text))
            await MainActor.run { NotificationCenter.default.post(name: .agenthailRequestAnswered, object: AnsweredRequest(requestID: requestID, choice: choice)) }
            return
        } catch {
            let answer = RequestAnswerFailure(error)
            failure = answer.dropsCard ? answer.message : "Agenthail could not send your answer: \(answer.message) Open the session to answer it."
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

struct AnsweredRequest: Sendable {
    let requestID: String
    let choice: String
}

extension Notification.Name {
    static let agenthailRequestAnswered = Notification.Name("AgenthailRequestAnswered")
}
