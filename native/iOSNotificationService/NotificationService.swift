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
        let arrived = Date()
        guard let host = content.userInfo["host"] as? String, !host.isEmpty,
              let credential = PairedCredential.read(host: host) else { return finish(with: nil) }
        let pushID = content.userInfo["pushId"] as? String ?? ""
        let requestID = content.userInfo["requestId"] as? String ?? ""
        let sessionID = content.userInfo["sessionId"] as? String ?? ""
        Task {
            async let receipted: Void = Self.sendReceipt(pushID: pushID, host: host, arrived: arrived, credential: credential)
            var answer: (String, RequestNotice)?
            if !requestID.isEmpty, !sessionID.isEmpty,
               let notice = await RequestNotifications.fetchNotice(endpoint: credential.endpoint, token: credential.token, sessionID: sessionID, requestID: requestID) {
                if !notice.actions.isEmpty {
                    await RequestCategoryRegistry.shared.register(requestID: requestID, actions: notice.actions, base: [])
                }
                answer = (requestID, notice)
            }
            _ = await receipted
            finish(with: answer)
        }
    }

    private static func sendReceipt(pushID: String, host: String, arrived: Date, credential: PairedHostCredential) async {
        guard !pushID.isEmpty else { return }
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        let alerts = PushReceipts.alerts(authorization: settings.authorizationStatus, alert: settings.alertSetting)
        await PushReceipts.send(PushReceipt(host: host, pushId: pushID, receivedAt: arrived, alerts: alerts), credential: credential)
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
