@testable import AgenthailHelpers
import Foundation

@main
struct RequestSyncKeyTest {
    static func main() {
        let writable = session(readOnly: false)
        let readOnly = session(readOnly: true)
        expect(SessionPane.requestSyncKey(writable) != SessionPane.requestSyncKey(readOnly), "a session turning read-only resyncs its requests, which clears a request error")
        expect(SessionPane.requestSyncKey(writable) == SessionPane.requestSyncKey(session(readOnly: false)), "an unchanged session keeps its key")
        print("request sync key tests passed")
    }

    static func session(readOnly: Bool) -> SessionState {
        let json = #"{"id":"s1","surface":"claude","name":"Lead","status":"idle","queueCount":0,"open":true,"current":true,"readOnly":"# + "\(readOnly)" + #","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true,"respond":true}}"#
        return try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
