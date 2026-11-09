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
        check(BackgroundTasks(running: true, tasks: [shell], source: "transcript").sourceNote == nil, "a list read from the transcript needs no note")
        check(BackgroundTasks(running: true, tasks: [], source: "status").sourceNote != nil, "a shell Claude reports without a list keeps its note")

        let working = BackgroundTasks(running: true, tasks: [shell, server], source: "host")
        let reordered = BackgroundTasks(running: true, tasks: [server, shell], source: "host")
        let added = BackgroundTasks(running: true, tasks: [shell, server, BackgroundTask(id: "a2", kind: "agent")], source: "host")
        let finished = BackgroundTasks(running: false, tasks: [server], source: "host")
        typealias Choices = BackgroundDockChoices
        check(Choices.shows(working, for: "s1", dismissed: ""), "the dock shows until dismissed")
        let hidden = Choices.dismissing(working, for: "s1", in: "")
        check(!Choices.shows(working, for: "s1", dismissed: hidden), "dismissing hides the dock")
        check(!Choices.shows(reordered, for: "s1", dismissed: hidden), "the same tasks in another order stay hidden")
        check(Choices.shows(working, for: "s2", dismissed: hidden), "dismissing one session leaves another's dock")
        check(Choices.shows(added, for: "s1", dismissed: hidden), "a new task brings the dock back")
        check(Choices.shows(finished, for: "s1", dismissed: hidden), "a task finishing brings the dock back")
        let again = Choices.dismissing(added, for: "s1", in: hidden)
        check(!Choices.shows(added, for: "s1", dismissed: again) && Choices.value("s1", in: again) == added.taskSet, "dismissing again replaces the session's entry")
        check(!Choices.shows(BackgroundTasks(running: false, tasks: [], source: "host"), for: "s1", dismissed: ""), "nothing to show stays hidden")
        check(Choices.value("s", in: hidden) == nil, "a session id that prefixes another is its own entry")
        let many = (0..<600).reduce("") { Choices.dismissing(working, for: "s\($1)", in: $0) }
        check(many.split(separator: "\n").count == Choices.remembered && Choices.value("s599", in: many) != nil && Choices.value("s0", in: many) == nil, "only the latest sessions are remembered")

        check(Choices.isOpen(working, for: "s1", choices: ""), "running tasks open the list")
        check(!Choices.isOpen(finished, for: "s1", choices: ""), "ambient tasks alone start collapsed")
        let collapsed = Choices.toggling(working, for: "s1", in: "")
        check(!Choices.isOpen(working, for: "s1", choices: collapsed), "the list collapses")
        check(Choices.isOpen(working, for: "s2", choices: collapsed), "collapsing one session leaves another open")
        check(Choices.isOpen(working, for: "s1", choices: Choices.toggling(working, for: "s1", in: collapsed)), "the list opens again")
        check(Choices.isOpen(finished, for: "s3", choices: Choices.toggling(finished, for: "s3", in: "")), "an ambient list opens")

        check(shell.sinceText() == nil && shell.timedLine() == "Shell: npm run build", "a task without a start time has no since")
        let now = Calendar.current.date(bySettingHour: 12, minute: 0, second: 0, of: Date())!
        let tenMinutesAgo = now.addingTimeInterval(-600)
        var started = shell
        started.since = ISO8601DateFormatter().string(from: tenMinutesAgo)
        let today = started.sinceText(now: now) ?? ""
        check(today == "since " + tenMinutesAgo.formatted(date: .omitted, time: .shortened), "a task started today shows its time: \(today)")
        check(started.timedLine(now: now) == "Shell: npm run build · \(today)", "the line ends with the since time")
        let threeDaysAgo = now.addingTimeInterval(-3 * 86_400)
        started.since = ISO8601DateFormatter().string(from: threeDaysAgo)
        let earlier = started.sinceText(now: now) ?? ""
        check(earlier == "since " + threeDaysAgo.formatted(.dateTime.weekday(.abbreviated).hour().minute()), "a task from an earlier day names the day: \(earlier)")
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
