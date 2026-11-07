@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptRowIDTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    static func main() {
        let item = TranscriptRowID.item("x")
        let tagged = [item, TranscriptRowID.tools("x"), TranscriptRowID.group("x"), TranscriptRowID.images("x"), TranscriptRowID.fold(item), TranscriptRowID.queue("x"), TranscriptRowID.failedAttachment("x"), TranscriptRowID.output("x"), TranscriptRowID.file(item, path: "x"), "more-" + item]
        check(Set(tagged.map { $0.prefix(2) }).count == tagged.count, "every kind of row key starts differently: \(tagged)")
        check(TranscriptRowID.file("t:a", path: "b:c") != TranscriptRowID.file("t:a:b", path: "c"), "a file key keeps its owner and path apart")
        print("ok   transcript_row_id_test")
    }
}
