import UserNotifications

final class NotificationService: UNNotificationServiceExtension, @unchecked Sendable {
    private let lock = NSLock()
    private var deliver: ((UNNotificationContent) -> Void)?
    private var content: UNMutableNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        let content = (request.content.mutableCopy() as? UNMutableNotificationContent) ?? UNMutableNotificationContent()
        lock.withLock {
            deliver = contentHandler
            self.content = content
        }
        guard let requestID = content.userInfo["requestId"] as? String, !requestID.isEmpty,
              let sessionID = content.userInfo["sessionId"] as? String, !sessionID.isEmpty,
              let credential = PairedCredential.read() else { return finish(with: nil) }
        Task {
            let notice = await RequestNotifications.fetchNotice(endpoint: credential.endpoint, token: credential.token, sessionID: sessionID, requestID: requestID)
            if let notice, !notice.actions.isEmpty {
                await RequestCategoryRegistry.shared.register(requestID: requestID, actions: notice.actions, base: [])
            }
            finish(with: notice.map { (requestID, $0) })
        }
    }

    override func serviceExtensionTimeWillExpire() {
        finish(with: nil)
    }

    private func finish(with answer: (requestID: String, notice: RequestNotice)?) {
        let (deliver, content) = lock.withLock {
            defer { self.deliver = nil }
            return (self.deliver, self.content)
        }
        guard let deliver, let content else { return }
        if let answer {
            content.subtitle = content.body
            content.body = answer.notice.prompt
            if !answer.notice.actions.isEmpty {
                content.categoryIdentifier = RequestNotifications.categoryIdentifier(requestID: answer.requestID)
            }
        }
        deliver(content)
    }
}
