@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptLinksTest {
    static func main() throws {
        let markdown = try AttributedString(markdown: """
        Open http://10.0.0.8:8317/management.html now, or `http://10.0.0.8:8317/status.html` in code, \
        or `run it at https://example.com/run here`, or [the docs](https://example.com/docs).

        Mail people@example.com, open file:///tmp/a.txt, or see (https://example.com/paren).

        ```
        https://example.com/fenced
        ```
        """)
        let linked = TranscriptLinks.linked(markdown)
        let links = linked.runs.compactMap { run in run.link.map { (String(linked[run.range].characters), $0.absoluteString, run.inlinePresentationIntent?.contains(.code) ?? false) } }
        func has(_ text: String, _ url: String, code: Bool) -> Bool { links.contains { $0.0 == text && $0.1 == url && $0.2 == code } }

        check(has("http://10.0.0.8:8317/management.html", "http://10.0.0.8:8317/management.html", code: false), "a bare address with a port is a link: \(links)")
        check(has("http://10.0.0.8:8317/status.html", "http://10.0.0.8:8317/status.html", code: true), "an address in a code span is a link that stays code: \(links)")
        check(has("https://example.com/run", "https://example.com/run", code: true), "an address inside a longer code span is linked alone: \(links)")
        check(has("the docs", "https://example.com/docs", code: false), "a written link keeps its text and target: \(links)")
        check(has("https://example.com/paren", "https://example.com/paren", code: false), "a closing parenthesis stays outside the link: \(links)")
        check(!links.contains { $0.1.contains("fenced") }, "a fenced code block is not linked: \(links)")
        let before = Set(markdown.runs.compactMap { run in run.link?.absoluteString })
        let added = Set(links.map(\.1)).subtracting(before)
        check(added.allSatisfy { $0.hasPrefix("http://") || $0.hasPrefix("https://") } && !added.contains { $0.hasPrefix("file") }, "only web addresses are added as links: \(added)")
        check(String(linked.characters) == String(markdown.characters), "linking never changes the text")

        let plain = TranscriptLinks.linked("Status is at http://10.0.0.8:8317/management.html.")
        let plainLinks = plain.runs.compactMap { run in run.link.map { _ in String(plain[run.range].characters) } }
        check(plainLinks == ["http://10.0.0.8:8317/management.html"], "a sentence's last period stays outside: \(plainLinks)")
        for (text, link) in [("see http://x/y.", "http://x/y"), ("(http://x/y)", "http://x/y"), ("see `http://x/y`.", "http://x/y")] {
            let parsed = TranscriptLinks.linked(try AttributedString(markdown: text))
            let found = parsed.runs.compactMap { run in run.link.map { _ in String(parsed[run.range].characters) } }
            check(found == [link], "\(text.debugDescription) links only \(link): \(found)")
        }
        for text in ["Staging is up at `https://example.com/staging`", "**https://example.com/staging**", "_https://example.com/staging_", "~~https://example.com/staging~~", "at https://example.com/staging`, then"] {
            let raw = TranscriptLinks.linked(text)
            let found = raw.runs.compactMap { run in run.link.map { (String(raw[run.range].characters), $0.absoluteString) } }
            check(found.count == 1 && found[0].0 == "https://example.com/staging" && found[0].1 == "https://example.com/staging", "plain \(text.debugDescription) leaves markdown marks out of the link: \(found)")
        }
        for address in ["https://example.com/user_name_", "https://example.com/~", "https://example.com/a*"] {
            for source in [TranscriptLinks.linked("open \(address) now"), TranscriptLinks.linked(try AttributedString(markdown: "open `\(address)` now"))] {
                let found = source.runs.compactMap { run in run.link.map { (String(source[run.range].characters), $0.absoluteString) } }
                check(found.count == 1 && found[0].0 == address && found[0].1 == address, "\(address) keeps its last character: \(found)")
            }
        }
        let bare = TranscriptLinks.linked("See: example.com, or See: https://example.com/x")
        let bareLinks = bare.runs.compactMap { run in run.link.map { $0.absoluteString } }
        check(bareLinks == ["https://example.com/x"], "a bare domain stays text even beside a colon: \(bareLinks)")
        let indented = TranscriptLinks.linked(try AttributedString(markdown: "Run this:\n\n    curl https://example.com/indented\n\nDone."))
        check(!indented.runs.contains { run in run.link != nil }, "an indented code block is not linked")
        let wideSource = try AttributedString(markdown: "**Family 👩‍👩‍👧 and cafe\u{301}**, then `see https://example.com/after here` done")
        check(!wideSource.runs.contains { run in run.link != nil }, "the code span starts without a link")
        let wide = TranscriptLinks.linked(wideSource)
        let wideLinks = wide.runs.compactMap { run in run.link.map { (String(wide[run.range].characters), $0.absoluteString) } }
        check(wideLinks.count == 1 && wideLinks[0].0 == "https://example.com/after" && wideLinks[0].1 == "https://example.com/after", "an address after emoji and combining marks in other runs is linked exactly: \(wideLinks)")
        check(TranscriptLinks.hasAddress("See `HTTPS://example.com`") && TranscriptLinks.hasAddress("http://10.0.0.8:8317") && !TranscriptLinks.hasAddress("See example.com"), "a message with a web address is one a row can open to reach it")
        check(TranscriptLinks.linked("no addresses here").runs.allSatisfy { run in run.link == nil }, "text without an address is unchanged")
        print("transcript links: ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
