@testable import AgenthailHelpers
import Foundation

@main
struct SessionConfigRootTest {
    static func main() {
        let signedIn = SessionConfigRoot(name: "work", root: "/Users/me/.claude-work", signedInAs: "work@example.com")
        expect(signedIn.summary == "work · currently signed in as work@example.com", "a config folder shows its current login")
        let unknown = SessionConfigRoot(name: "spare", root: "/Users/me/.claude-spare")
        expect(unknown.summary == "spare", "a folder with no login shows only its name")
        let decoded = try! JSONDecoder().decode(SessionConfigRoot.self, from: Data(#"{"name":"work","root":"/w","signedInAs":"work@example.com"}"#.utf8))
        expect(decoded.summary == signedIn.summary, "the row's signedInAs decodes")
        print("session config root tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL session_config_root_test: \(message)\n".utf8))
            exit(1)
        }
    }
}
