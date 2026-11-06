enum TranscriptRowID {
    static func item(_ id: String) -> String { "i:" + id }
    static func tools(_ first: String) -> String { "t:" + first }
    static func images(_ message: String) -> String { "m:" + message }
    static func fold(_ first: String) -> String { "f:" + first }
    static func file(_ owner: String, path: String) -> String { "file:\(owner.utf8.count):" + owner + path }
}
