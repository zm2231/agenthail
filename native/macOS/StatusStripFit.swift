import SwiftUI

enum StatusDensity: CaseIterable {
    case full, compact, icons, none
}

struct StatusStripFit<Lead: View, Tail: View>: View {
    let lead: Lead
    let tail: (StatusDensity) -> Tail

    init(@ViewBuilder lead: () -> Lead, @ViewBuilder tail: @escaping (StatusDensity) -> Tail) {
        self.lead = lead()
        self.tail = tail
    }

    var body: some View {
        HStack(spacing: DesktopSpace.l) {
            lead.layoutPriority(1)
            ViewThatFits(in: .horizontal) {
                tail(.full)
                tail(.compact)
                tail(.icons)
                tail(.none)
            }
        }
    }
}
