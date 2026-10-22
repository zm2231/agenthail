import SwiftUI

/// The free-text message a new session starts with. A grouped form lays a
/// titled field out as a label row with its text at the trailing edge, so the
/// title is the placeholder and the field spans the row, reading from the left.
struct FirstMessageField: View {
    @Binding var message: String

    var body: some View {
        TextField("First message", text: $message, prompt: Text("First message"), axis: .vertical)
            .labelsHidden()
            .multilineTextAlignment(.leading)
            .lineLimit(3...8)
    }
}
