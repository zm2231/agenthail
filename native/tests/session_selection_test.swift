import Foundation

@MainActor
final class SelectionProbe {
    var selectedID: String?
    var detailID: String?

    func load(_ id: String, delay: Duration) async {
        try? await Task.sleep(for: delay)
        if sessionLoadIsCurrent(id, selectedID: selectedID) {
            detailID = id
        }
    }
}

@main
struct SessionSelectionTest {
    @MainActor
    static func main() async {
        let probe = SelectionProbe()
        probe.selectedID = "A"
        async let first: Void = probe.load("A", delay: .milliseconds(120))
        probe.selectedID = "B"
        async let second: Void = probe.load("B", delay: .milliseconds(10))
        _ = await (first, second)
        guard probe.detailID == "B" else { exit(1) }
    }
}
