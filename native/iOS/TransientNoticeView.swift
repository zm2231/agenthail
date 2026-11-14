import SwiftUI
import UIKit

struct TransientNoticeView: View {
    static let navigationBar: CGFloat = 56

    let notice: TransientNotice

    var body: some View {
        ZStack {
            if let text = notice.text {
                Text(text)
                    .font(.subheadline)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
                    .background(.regularMaterial, in: Capsule())
                    .padding(.horizontal, 24)
                    .padding(.top, Self.navigationBar)
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
            if let text = notice.text { UIAccessibility.post(notification: .announcement, argument: text) }
        }
    }
}

extension View {
    func transientNotice(_ notice: TransientNotice) -> some View {
        overlay(alignment: .top) { TransientNoticeView(notice: notice) }
    }
}
