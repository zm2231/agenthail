import Foundation
import UserNotifications

enum RequestAnswer {
    static func send(choice: String, sessionID: String, requestID: String, api: () throws -> AgenthailAPI, center: UNUserNotificationCenter = .current()) async {
        let failure: String
        do {
            _ = try await api().respond(sessionID: sessionID, requestID: requestID, decision: .choice(choice))
            return
        } catch AgenthailAPIError.request(409, _) {
            failure = "That request was already answered or has expired."
        } catch {
            failure = "Agenthail could not send your answer: \(error.localizedDescription) Open the session to answer it."
        }
        let content = UNMutableNotificationContent()
        content.title = "Agenthail"
        content.body = failure
        content.userInfo = ["sessionId": sessionID]
        content.threadIdentifier = sessionID
        try? await center.add(UNNotificationRequest(identifier: "answer-" + requestID, content: content, trigger: nil))
    }
}
