@testable import AgenthailHelpers
import Foundation

@main
struct MainWindowShowTest {
    static func main() {
        check(MainWindowShow.step(visible: true, miniaturized: false) == .bringForward, "a link opened with the main window up brings it forward instead of opening it again")
        check(MainWindowShow.step(visible: false, miniaturized: true) == .bringForward, "a minimized main window is restored instead of opened again")
        check(MainWindowShow.step(visible: false, miniaturized: false) == .open, "a closed or hidden main window is opened")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
