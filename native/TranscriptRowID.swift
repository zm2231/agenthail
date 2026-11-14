enum TranscriptRowID {
    static func item(_ id: String) -> String { "i:" + id }
    static func tools(_ first: String) -> String { "t:" + first }
    static func group(_ first: String) -> String { "g:" + first }
    static func images(_ message: String) -> String { "m:" + message }
    static func fold(_ first: String) -> String { "f:" + first }
    static func queue(_ listID: String) -> String { "q:" + listID }
    static func failedAttachment(_ item: String) -> String { "a:" + item }
    static func output(_ result: String) -> String { "o:" + result }
    static func file(_ owner: String, path: String) -> String { "file:\(owner.utf8.count):" + owner + path }
    static func caption(_ item: String) -> String { "c:" + item }
}
