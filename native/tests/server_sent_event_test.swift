import Foundation

@main
struct ServerSentEventTest {
    static func main() {
        var parser = ServerSentEventParser()
        let framed = ["id: 7", "event: item", "data: {\"seq\":7}", ""].map { parser.consume($0) }
        expect(framed == [nil, nil, nil, "{\"seq\":7}"], "a blank line ends the event and yields its data: \(framed)")

        let multi = ["data: first", "data:second", ": keepalive", ""].map { parser.consume($0) }
        expect(multi.last == "first\nsecond", "data lines join with newlines and the space after the colon is optional")

        expect(parser.consume("") == nil, "a blank line with no pending data yields nothing")
        expect(parser.consume("data: unfinished") == nil, "data without a closing blank line is held")
        print("server-sent event tests passed")
    }

    private static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
