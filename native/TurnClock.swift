import Foundation

/// How a turn's length and a record's time read in a transcript.
enum TurnClock {
    /// "Finished 3:13 · 6:10 PM" from a host duration such as "3m13s" and the record's time.
    static func footer(duration: String?, at timestamp: String?) -> String {
        [duration.map { "Finished " + clock($0) }, time(timestamp)].compactMap { $0 }.joined(separator: " · ")
    }

    /// Minutes and seconds, or hours, minutes and seconds, from a duration such as "1h2m3s" or "12s".
    static func clock(_ duration: String) -> String {
        var parts: [Character: Int] = [:]
        var number = ""
        for character in duration {
            if character.isNumber { number.append(character) } else if let value = Int(number) {
                parts[character] = value
                number = ""
            } else {
                return duration
            }
        }
        guard number.isEmpty, !parts.isEmpty, Set(parts.keys).isSubset(of: ["h", "m", "s"]) else { return duration }
        let hours = parts["h"] ?? 0, minutes = parts["m"] ?? 0, seconds = parts["s"] ?? 0
        return hours > 0 ? String(format: "%d:%02d:%02d", hours, minutes, seconds) : String(format: "%d:%02d", minutes, seconds)
    }

    static func time(_ timestamp: String?) -> String? {
        guard let timestamp, let date = date(timestamp) else { return nil }
        return date.formatted(date: Calendar.current.isDateInToday(date) ? .omitted : .abbreviated, time: .shortened)
    }

    static func date(_ timestamp: String) -> Date? {
        (try? Date(timestamp, strategy: Date.ISO8601FormatStyle(includingFractionalSeconds: true))) ?? (try? Date(timestamp, strategy: Date.ISO8601FormatStyle()))
    }
}
