@testable import AgenthailHelpers
import Foundation

@main
struct ClaudeRemoteControlTest {
    static func main() {
        let body = #"{"available":true,"roots":[{"root":"/Users/a/.claude","name":"default","settingsPath":"/Users/a/.claude/settings.json","atStartup":true},{"root":"/Users/a/.claude-work","name":"claude-work","settingsPath":"/Users/a/.claude-work/settings.json"},{"root":"/Users/a/broken","name":"broken","settingsPath":"/Users/a/broken/settings.json","problem":"/Users/a/broken/settings.json is not a JSON object; fix it first"}]}"#
        guard let state = try? JSONDecoder().decode(ClaudeRemoteControlState.self, from: Data(body.utf8)) else {
            print("FAIL: the daemon's answer does not decode")
            exit(1)
        }
        check(state.available && state.roots.count == 3, "every root is listed")
        check(state.roots[0].atStartup == true, "a root that turned it on reads as on")
        check(state.roots[1].atStartup == nil && state.roots[1].problem == nil, "a root that leaves it to Claude reads as unset")
        check(state.roots[2].problem?.contains("not a JSON object") == true, "a root whose settings cannot be read says why")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
