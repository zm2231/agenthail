import AppKit
import SwiftUI

struct TransientNoticeView: View {
    let notice: TransientNotice

    var body: some View {
        ZStack {
            if let text = notice.text {
                Text(text)
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, DesktopSpace.m)
                    .padding(.vertical, DesktopSpace.s)
                    .floatingGlass(DesktopPalette.raised, in: Capsule())
                    .padding(.top, TitleBarStrip.height + DesktopSpace.s)
                    .onTapGesture { notice.dismiss() }
                    .accessibilityAddTraits(.isButton)
                    .accessibilityHint("Dismisses this message")
                    .accessibilityAction { notice.dismiss() }
                    .accessibilityIdentifier("project-notice")
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .animation(.snappy, value: notice.text)
        .onChange(of: notice.shown) {
            guard let text = notice.text else { return }
            NSAccessibility.post(element: NSApp as Any, notification: .announcementRequested, userInfo: [.announcement: text, .priority: NSAccessibilityPriorityLevel.high.rawValue])
        }
    }
}
