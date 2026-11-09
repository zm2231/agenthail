import Foundation

extension BackgroundTasks {
    var isShown: Bool { running || !(tasks ?? []).isEmpty }
    var isAmbientOnly: Bool { !running }
    var taskSet: String { ([running ? "running" : "ambient"] + (tasks ?? []).map(\.id).sorted()).joined(separator: ",") }
}

enum BackgroundDockChoices {
    static let dismissedKey = "backgroundTasks.dismissedSessions"
    static let openKey = "backgroundTasks.openSessions"
    static let remembered = 500

    static func value(_ sessionID: String, in stored: String) -> String? {
        stored.split(separator: "\n").lazy.compactMap { line -> String? in
            let parts = line.split(separator: "\t", maxSplits: 1)
            return parts.count == 2 && parts[0] == sessionID ? String(parts[1]) : nil
        }.first
    }

    static func setting(_ sessionID: String, to value: String, in stored: String) -> String {
        let kept = stored.split(separator: "\n").filter { !$0.hasPrefix(sessionID + "\t") }.map(String.init)
        return (kept + [sessionID + "\t" + value]).suffix(remembered).joined(separator: "\n")
    }

    static func shows(_ background: BackgroundTasks, for sessionID: String, dismissed stored: String) -> Bool {
        background.isShown && value(sessionID, in: stored) != background.taskSet
    }

    static func dismissing(_ background: BackgroundTasks, for sessionID: String, in stored: String) -> String {
        setting(sessionID, to: background.taskSet, in: stored)
    }

    static func isOpen(_ background: BackgroundTasks, for sessionID: String, choices stored: String) -> Bool {
        value(sessionID, in: stored).map { $0 == "open" } ?? !background.isAmbientOnly
    }

    static func toggling(_ background: BackgroundTasks, for sessionID: String, in stored: String) -> String {
        setting(sessionID, to: isOpen(background, for: sessionID, choices: stored) ? "closed" : "open", in: stored)
    }
}

extension BackgroundTask {
    func sinceText(now: Date = Date(), calendar: Calendar = .current) -> String? {
        guard let since, let date = TimestampParser.date(since) else { return nil }
        let time = calendar.isDate(date, inSameDayAs: now)
            ? date.formatted(date: .omitted, time: .shortened)
            : date.formatted(.dateTime.weekday(.abbreviated).hour().minute())
        return "since \(time)"
    }

    func timedLine(now: Date = Date()) -> String {
        sinceText(now: now).map { "\(line) · \($0)" } ?? line
    }
}
