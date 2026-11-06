import SwiftUI

extension View {
    func topFade(below inset: CGFloat, over length: CGFloat = 16) -> some View {
        mask {
            VStack(spacing: 0) {
                Color.clear.frame(height: inset)
                LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: .bottom).frame(height: length)
                Color.black
            }
        }
    }
}
