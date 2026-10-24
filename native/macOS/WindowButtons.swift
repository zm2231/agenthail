import AppKit

enum WindowButtons {
    static let kinds: [NSWindow.ButtonType] = [.closeButton, .miniaturizeButton, .zoomButton]
    static let spacing: CGFloat = 20
    static let span: CGFloat = 54
    static let paneInset: CGFloat = 12
    static let stripInset: CGFloat = 18
    static let bandHeight: CGFloat = 36
    static let toggleWidth: CGFloat = 20
    static let trailing = paneInset + span + 12

    static func headerInset(toggle: Bool) -> CGFloat {
        toggle ? trailing + toggleWidth + 12 : trailing
    }

    static func origins(center: CGPoint, size: CGSize, count: Int) -> [CGPoint] {
        (0..<count).map { CGPoint(x: center.x + CGFloat($0) * spacing, y: center.y - size.height / 2) }
    }

    static func place(in window: NSWindow, band: CGRect, leading: CGFloat) {
        for (index, kind) in kinds.enumerated() {
            guard let button = window.standardWindowButton(kind), let container = button.superview else { continue }
            let start = container.convert(NSPoint(x: band.minX + leading, y: band.midY), from: nil)
            let origin = origins(center: start, size: button.frame.size, count: kinds.count)[index]
            if button.frame.origin != origin { button.setFrameOrigin(origin) }
        }
    }
}
