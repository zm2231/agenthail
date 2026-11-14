@testable import AgenthailHelpers
import Foundation

final class SentFileStub: URLProtocol {
    nonisolated(unsafe) static var requests = 0
    nonisolated(unsafe) static var body = Data("%PDF-1.4 report".utf8)
    nonisolated(unsafe) static var length: String?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        SentFileStub.requests += 1
        var headers = ["Content-Type": "application/pdf"]
        if let length = SentFileStub.length { headers["Content-Length"] = length }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: headers)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: SentFileStub.body)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@main
struct SentFilesTest {
    static func item(_ json: String) -> TimelineItem {
        try! JSONDecoder().decode(TimelineItem.self, from: Data(json.utf8))
    }

    @MainActor
    static func main() async {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("sent-files-test-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let cache = SentFileCache(root: root)

        let names: [(String, String)] = [
            ("report.pdf", "report.pdf"),
            ("../../etc/passwd", "etcpasswd"),
            ("/abs/notes.md", "absnotes.md"),
            ("C:\\Users\\x\\a.txt", "CUsersxa.txt"),
            ("..", "file"),
            ("...", "file"),
            (".", "file"),
            ("", "file"),
            ("   ", "file"),
            (".hidden", "hidden"),
            ("..evil.sh", "evil.sh"),
            ("a\u{0}b\nc\u{1B}d.txt", "abcd.txt"),
            ("résumé 2026.pdf", "résumé 2026.pdf"),
        ]
        for (raw, want) in names {
            let got = SentFileName.safe(raw)
            check(got == want, "\(raw.debugDescription) becomes \(want.debugDescription), got \(got.debugDescription)")
            let path = cache.location(id: "abc", name: raw).standardizedFileURL.path
            check(path.hasPrefix(root.appendingPathComponent("abc").standardizedFileURL.path + "/") && !path.dropFirst(root.path.count).contains(".."), "\(raw.debugDescription) stays in its folder: \(path)")
        }
        let long = SentFileName.safe(String(repeating: "x", count: 300) + ".pdf")
        check(long.count == SentFileName.maxLength && long.hasSuffix(".pdf"), "a long name is cut to \(SentFileName.maxLength) keeping its extension, got \(long.count)")
        check(SentFileName.safe(String(repeating: "y", count: 300)).count == SentFileName.maxLength, "a long name without an extension is cut too")
        check(SentFileName.folder("../x/y") == "xy" && SentFileName.folder("..") == "attachment", "an attachment id becomes one plain folder name")

        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SentFileStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://daemon.invalid")!, token: "t", session: URLSession(configuration: config))
        let pdf = SessionAttachment(id: "f1", mediaType: "application/pdf", width: nil, height: nil, bytes: 15, name: "report.pdf")
        do {
            let url = try await api.downloadSentFile(sessionID: "s1", attachment: pdf, name: "../report.pdf", cache: cache)
            check(url.lastPathComponent == "report.pdf" && url.deletingLastPathComponent().lastPathComponent == "f1", "the file lands in Caches/SentFiles/<id>/<name>: \(url.path)")
            check((try? Data(contentsOf: url)) == SentFileStub.body, "the bytes are the route's")
            check(SentFileCache.isQuarantined(url), "the downloaded copy is quarantined")
            _ = try await api.downloadSentFile(sessionID: "s1", attachment: pdf, name: "../report.pdf", cache: cache)
            check(SentFileStub.requests == 1, "a cached copy is reused, got \(SentFileStub.requests) requests")
        } catch {
            check(false, "the download failed: \(error)")
        }
        let big = SessionAttachment(id: "f2", mediaType: "application/pdf", width: nil, height: nil, bytes: 15, name: "big.pdf")
        SentFileStub.length = String(SentFileCache.fileLimit + 1)
        do {
            _ = try await api.downloadSentFile(sessionID: "s1", attachment: big, name: "big.pdf", cache: cache)
            check(false, "a file announced over the limit is refused")
        } catch {
            check(error.localizedDescription.contains("too large"), "the refusal says why: \(error.localizedDescription)")
        }
        SentFileStub.length = nil
        do {
            _ = try await api.downloadSentFile(sessionID: "s1", attachment: big, name: "big.pdf", cache: cache, limit: 4)
            check(false, "a file that grows past the limit is refused")
        } catch {
            check(error.localizedDescription.contains("too large"), "the running size refuses it: \(error.localizedDescription)")
        }
        check(cache.cached(id: "f2", name: "big.pdf") == nil, "a refused file leaves nothing in the cache")

        let fm = FileManager.default
        let now = Date()
        for (name, age, size) in [("old", 8.0, 10), ("middle", 3.0, 600), ("recent", 1.0, 600), ("newest", 0.0, 100)] {
            let folder = root.appendingPathComponent("prune-" + name)
            try? fm.createDirectory(at: folder, withIntermediateDirectories: true)
            fm.createFile(atPath: folder.appendingPathComponent("f").path, contents: Data(count: size))
            try? fm.setAttributes([.modificationDate: now.addingTimeInterval(-age * 24 * 3600)], ofItemAtPath: folder.path)
        }
        let removed = Set(cache.prune(now: now, maxBytes: 1000).map(\.lastPathComponent))
        check(removed.contains("prune-old"), "a folder unused for over 7 days is removed: \(removed)")
        check(removed.contains("prune-middle") && !removed.contains("prune-recent") && !removed.contains("prune-newest"), "over the size cap the least recently used go first until it fits: \(removed)")

        let file = item(#"{"id":"r1-0-file","truncated":false,"kind":"file","role":"assistant","title":"report.pdf","text":"Here is the report","callId":"toolu_1","files":[{"name":"report.pdf","path":"/srv/out/report.pdf","bytes":1200}],"attachment":{"id":"f1","mediaType":"application/pdf","bytes":1200,"name":"report.pdf"}}"#)
        let gone = item(#"{"id":"r1-1-file","truncated":false,"kind":"file","role":"assistant","title":"old.md","text":"","callId":"toolu_1","files":[{"name":"old.md","path":"/srv/out/old.md"}]}"#)
        let huge = item(#"{"id":"r1-2-file","truncated":false,"kind":"file","role":"assistant","title":"dump.bin","text":"","callId":"toolu_1","files":[{"name":"dump.bin","path":"/srv/out/dump.bin","bytes":99000000}]}"#)
        let markdown = item(#"{"id":"r1-3-file","truncated":false,"kind":"file","role":"assistant","title":"notes.md","text":"","files":[{"name":"notes.md","path":"/srv/notes.md","bytes":20}],"attachment":{"id":"f3","mediaType":"text/markdown","bytes":20,"name":"notes.md"}}"#)
        check(SentFileState.reason(file, hostName: "build") == nil && SentFileState.isPDF(file) && SentFileState.icon(file) == "doc.richtext", "a servable PDF opens")
        check(SentFileState.reason(gone, hostName: "build") == "No longer on build.", "a file without bytes is gone: \(SentFileState.reason(gone, hostName: "build") ?? "")")
        check(SentFileState.reason(huge, hostName: "build")?.hasPrefix("Too large to open here") == true, "a file over 50 MB says so")
        check(SentFileState.icon(markdown) == "doc.plaintext" && !SentFileState.isPDF(markdown), "markdown reads as text")
        check(SentFileState.caption(file) == "Here is the report" && SentFileState.caption(gone) == nil, "the caption is on the first file only")

        let image = item(#"{"id":"r2-0","truncated":false,"kind":"attachment","role":"assistant","title":"Image","text":"The chart","callId":"toolu_2","attachment":{"id":"i1","mediaType":"image/png","width":10,"height":10}}"#)
        let blocks = TranscriptBlock.build([file, gone, image])
        let kinds = blocks.map { block -> String in
            switch block.kind {
            case .assistant(let text): return "text:" + text
            case .sentFile(let item): return "file:" + item.title
            case .image: return "image"
            default: return "other"
            }
        }
        check(kinds == ["text:Here is the report", "file:report.pdf", "file:old.md", "text:The chart", "image"], "a sent file is a card after its caption, and a sent image shows its caption: \(kinds)")
        check(Set(blocks.map(\.id)).count == blocks.count, "every block keeps its own id")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
