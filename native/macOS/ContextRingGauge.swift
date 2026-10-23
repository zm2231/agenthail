import SwiftUI

struct ContextRingGauge: View {
    let context: ContextState

    var body: some View {
        let tint = switch context.level {
        case .normal: DesktopPalette.text2
        case .high: DesktopPalette.attention
        case .full: DesktopPalette.failed
        }
        ZStack {
            Circle().stroke(DesktopPalette.line, lineWidth: 2)
            Circle()
                .trim(from: 0, to: min(context.fraction ?? (context.level == .full ? 1 : 0), 1))
                .stroke(tint, style: StrokeStyle(lineWidth: 2, lineCap: .round))
                .rotationEffect(.degrees(-90))
        }
        .frame(width: 14, height: 14)
    }
}
