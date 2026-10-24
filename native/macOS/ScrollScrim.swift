import SwiftUI

enum ScrollScrim {
    /// nil while content fills the viewport, so no scroll offset can shrink the region.
    static func height(content: CGFloat, viewport: CGFloat) -> CGFloat? {
        content < viewport ? max(content.rounded(), 0) : nil
    }
}

extension View {
    func scrollScrim() -> some View {
        modifier(ScrollScrimRegion())
    }
}

private struct ScrollScrimRegion: ViewModifier {
    @State private var height: CGFloat?

    func body(content: Content) -> some View {
        content
            .background {
                Color.clear
                    .frame(height: height)
                    .scrimText()
                    .frame(maxHeight: .infinity, alignment: .top)
            }
            .onScrollGeometryChange(for: CGFloat?.self) { geometry in
                ScrollScrim.height(content: geometry.contentSize.height, viewport: geometry.containerSize.height)
            } action: { _, height in
                self.height = height
            }
    }
}
