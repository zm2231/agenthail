@testable import AgenthailHelpers

@main
struct ElsewhereWaitCardTest {
    static func main() {
        let cases: [(kind: String, surface: String, heading: String)] = [
            ("question", "claude", "Claude asks you a question"),
            ("input", "codex", "Codex needs your input"),
            ("approval", "codex", "Codex needs your approval"),
        ]
        for test in cases {
            let heading = ElsewhereWaitCard.heading(kind: test.kind, surface: test.surface)
            guard heading == test.heading else {
                fatalError("\(test.kind) on \(test.surface): \(heading)")
            }
        }
        print("elsewhere wait card headings ok")
    }
}
