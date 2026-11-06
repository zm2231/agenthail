import SwiftUI

extension StructuredText {
  struct BlockQuote: View {
    @Environment(\.blockQuoteStyle) private var blockQuoteStyle

    private let intent: PresentationIntent.IntentType?
    private let content: AttributedSubstring

    init(intent: PresentationIntent.IntentType?, content: AttributedSubstring) {
      self.intent = intent
      self.content = content
    }

    var body: some View {
      ForwardsBlockSpacing(inner: BlockContent(parent: intent, content: content)) { block in
        let configuration = BlockStyleConfiguration(
          label: .init(block),
          indentationLevel: indentationLevel
        )
        AnyView(blockQuoteStyle.resolve(configuration: configuration))
      }
    }

    private var indentationLevel: Int {
      content.runs.first?.presentationIntent?.indentationLevel ?? 0
    }
  }
}
