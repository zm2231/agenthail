@preconcurrency import UserNotifications
import XCTest
@testable import Agenthail

final class NotificationDelegateQueueTests: XCTestCase {
    private typealias WillPresent = @convention(c) (AnyObject, Selector, UNUserNotificationCenter, UNNotification?, @escaping @convention(block) (UNNotificationPresentationOptions) -> Void) -> Void

    func testTheNotificationDelegateAnswersWhenIOSCallsItOffTheMainQueue() async {
        let delegate = await IOSAppDelegate()
        let selector = NSSelectorFromString("userNotificationCenter:willPresentNotification:withCompletionHandler:")
        guard let method = class_getInstanceMethod(IOSAppDelegate.self, selector) else { return XCTFail("the delegate does not answer willPresent") }
        let willPresent = unsafeBitCast(method_getImplementation(method), to: WillPresent.self)
        let center = UNUserNotificationCenter.current()
        let options = await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                willPresent(delegate, selector, center, nil) { continuation.resume(returning: $0) }
            }
        }
        XCTAssertEqual(options, [.banner, .sound, .badge])
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
