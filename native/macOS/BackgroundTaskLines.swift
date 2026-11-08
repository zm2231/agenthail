import Foundation

extension BackgroundTasks {
    var isShown: Bool { running || !(tasks ?? []).isEmpty }
    var isAmbientOnly: Bool { !running }
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
