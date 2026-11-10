@testable import AgenthailHelpers
import Foundation

@main
struct PathNamesTest {
    static func main() {
        let folders = ["/work/agenthail/": "agenthail", "/": "/", "": "/", "//": "/", "/a//b": "b", ".": "/", "relative/dir": "dir", "/a b/c d": "c d", "/home/demo/work/agenthail": "agenthail", "/a/./b/.": "b"]
        for (path, name) in folders {
            check(PathNames.last(path) == name, "folder of \(path.debugDescription) is \(PathNames.last(path).debugDescription), not \(name.debugDescription)")
        }
        check(PathNames.last("/nonexistent-\(UUID().uuidString)/agenthail") == "agenthail", "a folder that does not exist is still named")
        FileManager.default.changeCurrentDirectoryPath(NSTemporaryDirectory())
        check(PathNames.last("") == "/" && PathNames.last(".") == "/", "the working directory does not change a folder name")
        check(PathNames.components("/home/demo/work/agenthail/") == ["home", "demo", "work", "agenthail"], "components drop the root and a trailing slash")
        check(PathNames.components("/") == [] && PathNames.components("") == [], "the root has no components")
        check(PathNames.components("a//b/./c") == ["a", "b", "c"], "empty and current-folder parts are dropped")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
