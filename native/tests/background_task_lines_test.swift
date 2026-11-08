@testable import AgenthailHelpers
import Foundation

@main
struct BackgroundTaskLinesTest {
    static func main() {
        let shell = BackgroundTask(id: "b1", kind: "shell", description: "npm run build")
        let server = BackgroundTask(id: "m1", kind: "shell", description: "npm run dev", ambient: true)

        check(!BackgroundTasks(running: false, tasks: [], source: "host").isShown, "nothing running and no tasks shows no dock")
        check(BackgroundTasks(running: true, tasks: [], source: "status").isShown, "background work without a list still shows, as on the dashboard")
        check(BackgroundTasks(running: false, tasks: [server], source: "host").isShown, "an ambient task alone still shows")
        check(BackgroundTasks(running: false, tasks: [server], source: "host").isAmbientOnly, "only ambient tasks keep the dock to one line")
        check(!BackgroundTasks(running: true, tasks: [shell, server], source: "host").isAmbientOnly, "a running task opens the full list")
        check(BackgroundTasks(running: false, tasks: [server], source: "host").summary == "Only ambient tasks run", "the compact line uses the dashboard's words")
        check(BackgroundTasks(running: true, tasks: [], source: "runtime").sourceNote == nil, "a list from the runtime needs no note, like the host's")

        check(shell.sinceText() == nil && shell.timedLine() == "Shell: npm run build", "a task without a start time has no since")
        let now = Date(timeIntervalSince1970: 1_767_268_800)
        var started = shell
        started.since = ISO8601DateFormatter().string(from: now.addingTimeInterval(-600))
        let today = started.sinceText(now: now) ?? ""
        check(today.hasPrefix("since ") && !today.contains(","), "a task started today shows its time: \(today)")
        check(started.timedLine(now: now) == "Shell: npm run build · \(today)", "the line ends with the since time")
        started.since = ISO8601DateFormatter().string(from: now.addingTimeInterval(-3 * 86_400))
        let earlier = started.sinceText(now: now) ?? ""
        check(earlier.hasPrefix("since ") && earlier != today, "a task from an earlier day names the day: \(earlier)")
        var ambient = server
        ambient.since = "not a time"
        check(ambient.timedLine() == "Shell: npm run dev (ambient)", "an unreadable start time is left out")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
