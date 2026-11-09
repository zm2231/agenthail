import SwiftUI

struct IOSCheckoutRow: View {
    let checkout: SessionTree.Checkout
    var host: String? = nil

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.triangle.branch")
            Text(checkout.label)
                .font(.caption.monospaced())
                .lineLimit(1)
                .truncationMode(.middle)
            if let host { IOSHostBadge(name: host) }
            if checkout.dirty { IOSDirtyDot() }
            Spacer(minLength: 0)
        }
        .font(.caption)
        .foregroundStyle(PhonePalette.muted)
        .frame(maxWidth: .infinity, alignment: .leading)
        .listRowInsets(EdgeInsets(top: 10, leading: 20, bottom: 0, trailing: 20))
        .listRowSeparator(.hidden)
        .listRowBackground(GlassRowBackground(selected: false))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Self.spoken(checkout, host: host))
        .accessibilityIdentifier("checkout-" + checkout.id)
    }

    static func spoken(_ checkout: SessionTree.Checkout, host: String?) -> String {
        "Worktree \(checkout.label)" + (host.map { " on \($0)" } ?? "") + (checkout.dirty ? ", uncommitted changes" : "")
    }
}

struct IOSDirtyDot: View {
    var body: some View {
        Circle().fill(PhonePalette.muted).frame(width: 5, height: 5).accessibilityHidden(true)
    }
}
