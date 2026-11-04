#if DEBUG
import Foundation
import os

enum InboxPreview {
    private static let requests = OSAllocatedUnfairLock(initialState: 0)

    static func nextPage() -> String {
        let count = requests.withLock { value in
            value += 1
            return value
        }
        let newest = #"{"id":14,"sessionId":"demo","target":"Make the build reliable","sourceSessionId":"codex-demo","source":"@reviewer","message":"A message that just arrived.","status":"delivered","evidence":"delivered","pending":false,"createdAt":"2026-09-12T05:10:00Z"},"#
        return #"{"items":["# + (count > 2 ? newest : "") + rows + "]}"
    }

    private static let rows = #"""
    {"id":13,"sessionId":"demo","target":"Make the build reliable","source":"@reviewer","message":"[Claude peer message from reviewer]\n<cross-session-message from=\"uds:/tmp/peer.sock\" from-name=\"@reviewer\">The signing step passes now.</cross-session-message>","status":"delivered","evidence":"delivered","pending":false,"createdAt":"2026-09-12T05:00:00Z"},{"id":12,"sessionId":"codex-demo","target":"Review the release pipeline","source":"agenthail-person","message":"Verify the signing step before the next release.","status":"unknown","evidence":"unknown","pending":false,"queueId":9,"queueStatus":"dead","attempts":1,"lastError":"Delivery outcome is unknown. Check the session before retrying.","createdAt":"2026-09-10T18:20:00Z"},{"id":11,"sessionId":"demo","target":"Make the build reliable","message":"Waiting in the queue, so Current shows it.","status":"queued","evidence":"queued","pending":true,"queueId":1,"queueStatus":"pending","createdAt":"2026-09-12T04:00:00Z"}
    """#
}
#endif
