import Foundation

@main
struct ComposerDropTest {
    static func main() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let plain = URL(fileURLWithPath: "\(home)/work/notes.md")
        let spaced = URL(fileURLWithPath: "/tmp/My Files/it's.png")
        check(ComposerDrop.text(for: [plain]) == "~/work/notes.md", "a home path is shortened")
        check(ComposerDrop.text(for: [plain, spaced]) == #"~/work/notes.md '/tmp/My Files/it'\''s.png'"#, "paths with spaces or quotes are quoted")
        check(ComposerDrop.text(for: [URL(string: "https://example.com")!]) == nil, "web links are ignored")
        check(ComposerDrop.text(for: [plain, URL(string: "https://example.com")!]) == nil, "a drop with any web link is refused")
        check(ComposerDrop.text(for: [URL(fileURLWithPath: "\(home)/My Files/x;y.md")]) == "~/'My Files/x;y.md'", "a home path keeps ~ outside the quotes")
        for name in ["a&b", "a*b", "a?b", "a[b]", "a(b)", "a|b", "a<b", "a#b", "a!b", "a\nb"] {
            check(ComposerDrop.text(for: [URL(fileURLWithPath: "/tmp/\(name)")]) == "'/tmp/\(name)'", "\(name) is quoted")
        }
        check(ComposerDrop.insert("a.md", into: "") == "a.md", "a drop fills an empty draft")
        check(ComposerDrop.insert("a.md", into: "Read") == "Read a.md", "a drop is separated from typed text")
        check(ComposerDrop.insert("a.md", into: "Read\n") == "Read\na.md", "existing trailing space is kept")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
