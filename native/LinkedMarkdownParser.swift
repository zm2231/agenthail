import Foundation
import Textual

struct LinkedMarkdownParser: MarkupParser {
    private let markdown = AttributedStringMarkdownParser.markdown()

    func attributedString(for input: String) throws -> AttributedString {
        TranscriptLinks.linked(try markdown.attributedString(for: input))
    }
}

extension MarkupParser where Self == LinkedMarkdownParser {
    static var linkedMarkdown: Self { .init() }
}

struct LinkedPlainParser: MarkupParser {
    func attributedString(for input: String) throws -> AttributedString {
        TranscriptLinks.linked(input)
    }
}

extension MarkupParser where Self == LinkedPlainParser {
    static var linkedPlain: Self { .init() }
}
