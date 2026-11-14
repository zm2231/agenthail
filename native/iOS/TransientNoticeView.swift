import SwiftUI
import UIKit

struct TransientNoticeView: View {
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
                    .padding(.top, 4)
                    .onTapGesture { notice.dismiss() }
                    .accessibilityIdentifier("project-notice")
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .animation(.snappy, value: notice.text)
        .onChange(of: notice.text) { _, text in
            if let text { UIAccessibility.post(notification: .announcement, argument: text) }
        }
    }
}

extension View {
    func transientNotice(_ notice: TransientNotice) -> some View {
        overlay(alignment: .top) { TransientNoticeView(notice: notice) }
    }
}
