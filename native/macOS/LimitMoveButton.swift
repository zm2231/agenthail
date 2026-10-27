import SwiftUI

struct LimitMoveButton: View {
    let offer: LimitResumeState.MoveOffer
    let move: (String) -> Void
    @State private var confirming = false

    var body: some View {
        Button("Move to \(offer.name)…") { confirming = true }
            .help(offer.reason.map { "Continue this session under \(offer.name): \($0)" } ?? "Continue this session under \(offer.name)")
            .confirmationDialog("Move this session to \(offer.name)?", isPresented: $confirming) {
                Button("Move to \(offer.name)") { move(offer.root) }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text(["It stops here and continues under that account.", offer.reason.map { "Chosen for \($0)." }].compactMap { $0 }.joined(separator: " "))
            }
    }
}
