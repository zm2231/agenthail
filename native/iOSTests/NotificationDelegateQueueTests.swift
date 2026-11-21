@preconcurrency import UserNotifications
import XCTest
@testable import Agenthail

final class NotificationDelegateQueueTests: XCTestCase {
    private typealias WillPresent = @convention(c) (AnyObject, Selector, UNUserNotificationCenter, UNNotification?, @escaping @convention(block) (UNNotificationPresentationOptions) -> Void) -> Void

    func testTheNotificationDelegateAnswersWhenIOSCallsItOffTheMainQueue() async throws {
        let delegate = await IOSAppDelegate()
        let selector = NSSelectorFromString("userNotificationCenter:willPresentNotification:withCompletionHandler:")
        guard let method = class_getInstanceMethod(IOSAppDelegate.self, selector) else { return XCTFail("the delegate does not answer willPresent") }
        let willPresent = unsafeBitCast(method_getImplementation(method), to: WillPresent.self)
        let center = UNUserNotificationCenter.current()
        let shown = HostCatalog.qualify("studio", "s1")
        let present: ([AnyHashable: Any]) async throws -> UNNotificationPresentationOptions = { userInfo in
            let notification = try XCTUnwrap(Self.delivered(userInfo), "UNNotification can no longer be made in a test")
            return await withCheckedContinuation { continuation in
                DispatchQueue.global().async {
                    willPresent(delegate, selector, center, notification) { continuation.resume(returning: $0) }
                }
            }
        }
        let shownInfo: [AnyHashable: Any] = ["sessionId": "s1", "host": "studio"]
        let presentedOff = try await present(shownInfo)
        XCTAssertEqual(presentedOff, [.banner, .sound, .badge])
        OnScreenSessions.shared.appeared(shown)
        defer { OnScreenSessions.shared.disappeared(shown) }
        let presentedOn = try await present(shownInfo)
        XCTAssertEqual(presentedOn, [.badge], "the delegate reads the session the app shows")
    }

    private static func delivered(_ userInfo: [AnyHashable: Any]) -> UNNotification? {
        let content = UNMutableNotificationContent()
        content.userInfo = userInfo
        let request = UNNotificationRequest(identifier: "test", content: content, trigger: nil)
        let factory = NSSelectorFromString("notificationWithRequest:date:")
        guard UNNotification.responds(to: factory) else { return nil }
        return UNNotification.perform(factory, with: request, with: Date())?.takeUnretainedValue() as? UNNotification
    }

    func testTappingAFailedAnswerNoticeOpensItsSession() {
        let notice = RequestAnswer.failureNotice(RequestAnswerFailure(URLError(.timedOut)), text: "ship it", sessionID: "s1", host: "studio")
        XCTAssertEqual(IOSAppDelegate.openedSession(notice.userInfo), HostCatalog.qualify("studio", "s1"), "the notice names the computer, so a tap can open the session")
    }

    @MainActor
    func testANotificationForTheSessionOnScreenOnlyBadges() {
        let onScreen = OnScreenSessions()
        let model = AgenthailIOSModel(autoConnect: false, onScreen: onScreen)
        let shown = HostCatalog.qualify("studio", "s1")
        let about: ([AnyHashable: Any]) -> UNNotificationPresentationOptions = { IOSAppDelegate.presentation($0, onScreen: onScreen.contains) }
        model.sessionAppeared(shown)
        XCTAssertEqual(about(["sessionId": "s1", "host": "studio"]), [.badge], "the session on screen already shows what the notification says")
        XCTAssertEqual(about(["sessionId": "s2", "host": "studio"]), [.banner, .sound, .badge], "another session's notification still shows")
        model.sessionDisappeared(shown)
        XCTAssertEqual(about(["sessionId": "s1", "host": "studio"]), [.banner, .sound, .badge], "once the session leaves the screen its notifications show again")
    }
}
