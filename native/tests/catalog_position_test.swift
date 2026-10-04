import Foundation

@main
struct CatalogPositionTest {
    static func main() {
        var position = CatalogPosition(epoch: nil, cursor: 0)
        expect(position.adopt(snapshotEpoch: "A", snapshotSeq: 100), "first snapshot adopts its epoch")
        expect(position.cursor == 100, "cursor starts at the snapshot sequence")
        expect(!position.accept(100) && position.accept(101), "events at or before the cursor are ignored")
        expect(!position.adopt(snapshotEpoch: "A", snapshotSeq: 90) && position.cursor == 101, "same epoch never moves backwards")
        expect(position.adopt(snapshotEpoch: "B", snapshotSeq: 1) && position.cursor == 1, "a new epoch resets the cursor")
        expect(position.accept(2), "the new epoch's next event applies")

        let caps = Capabilities()
        func session(_ id: String, current: Bool) -> SessionState {
            SessionState(id: id, surface: "codex", name: id, alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: current, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil)
        }
        let sessions = [session("a", current: false), session("b", current: true)]
        expect(reconciledSelection(selected: "a", sessions: sessions) == "a", "a present selection is kept")
        expect(reconciledSelection(selected: "gone", sessions: sessions) == "b", "a removed selection moves to a current session")
        expect(reconciledSelection(selected: nil, sessions: []) == nil, "no sessions means no selection")
        print("catalog position tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
