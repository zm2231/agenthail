import Foundation

@main
struct ComposerDropTest {
    static func main() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let plain = URL(fileURLWithPath: "\(home)/work/notes.md")
        let spaced = URL(fileURLWithPath: "/tmp/My Files/it's.png")
        let message = { (urls: [URL]) in ComposerDrop.message(text: "", attachments: urls) }
        check(message([plain]) == "~/work/notes.md", "a home path is shortened")
        check(message([URL(fileURLWithPath: home)]) == "~", "the home folder itself is ~")
        check(message([plain, spaced]) == "~/work/notes.md\n'/tmp/My Files/it'\\''s.png'", "one path per line, quoted when needed")
        check(ComposerDrop.files([URL(string: "https://example.com")!]) == nil, "web links are ignored")
        check(ComposerDrop.files([plain, URL(string: "https://example.com")!]) == nil, "a drop with any web link is refused")
        check(ComposerDrop.files([]) == nil, "an empty drop is refused")
        check(message([URL(fileURLWithPath: "\(home)/My Files/x;y.md")]) == "~/'My Files/x;y.md'", "a home path keeps ~ outside the quotes")
        for name in ["a&b", "a*b", "a?b", "a[b]", "a(b)", "a|b", "a<b", "a#b", "a!b", "a\nb"] {
            check(message([URL(fileURLWithPath: "/tmp/\(name)")]) == "'/tmp/\(name)'", "\(name) is quoted")
        }
        check(ComposerDrop.message(text: "Read these\n", attachments: [plain]) == "Read these\n\n~/work/notes.md", "paths follow the typed text after a blank line")
        check(ComposerDrop.message(text: "  as typed ", attachments: []) == "  as typed ", "text without attachments is sent as typed")
        check(ComposerDrop.adding([plain, URL(fileURLWithPath: "\(home)/work/./notes.md")], to: [plain]) == [plain], "the same file is attached once")
        check(ComposerDrop.isEmpty(text: " \n", attachments: [plain]) == false && ComposerDrop.isEmpty(text: " ", attachments: []), "attachments count as content")
        check(ComposerDrop.isImage(spaced) && !ComposerDrop.isImage(plain), "images are recognised by type")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
