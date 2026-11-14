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
}
