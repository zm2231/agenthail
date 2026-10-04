import Foundation

@main
struct SessionHandleTest {
    static func main() {
        check(SessionHandle.normalized("  @builder ") == "builder", "a leading @ and outer spaces are dropped")
        check(SessionHandle.problem(with: "@builder") == nil, "a simple handle is valid")
        check(SessionHandle.problem(with: "  ") != nil, "an empty name is refused")
        check(SessionHandle.problem(with: "@") != nil, "a bare @ is refused")
        for bad in ["two words", "a/b", "a#b", "tab\tname"] {
            check(SessionHandle.problem(with: bad) != nil, "\(bad) is refused")
        }
        check(SessionHandle.problem(with: String(repeating: "a", count: 80)) == nil, "80 characters is allowed")
        check(SessionHandle.problem(with: String(repeating: "a", count: 81)) != nil, "81 characters is refused")
        check(SessionHandle.problem(with: String(repeating: "é", count: 40)) == nil, "80 bytes of accented letters is allowed")
        check(SessionHandle.problem(with: String(repeating: "🚀", count: 21)) != nil, "names are limited by the daemon's 80-byte rule")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
