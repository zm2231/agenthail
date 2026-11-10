#if DEBUG
import Foundation

enum SessionOperationsPreview {
    nonisolated static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-codex-ops") }
    nonisolated static var readOnly: Bool { ProcessInfo.processInfo.arguments.contains("--preview-read-only") }
    nonisolated static let readOnlyReason = "This session is shared read-only."

    nonisolated static func apply(session: inout [String: Any], capabilities: inout [String: Any]) {
        session["surface"] = "codex"
        session["runtimeKind"] = "claude-host"
        capabilities["lifecycle"] = ["status": true, "logs": true, "stop": true, "resume": true]
        if readOnly {
            session["readOnly"] = true
            session["readOnlyReason"] = readOnlyReason
        }
    }

    nonisolated static func apply(detail: inout [String: Any]) {
        var session = detail["session"] as! [String: Any]
        var capabilities = detail["capabilities"] as! [String: Any]
        apply(session: &session, capabilities: &capabilities)
        detail["session"] = session
        detail["capabilities"] = capabilities
        if readOnly {
            detail["readOnly"] = true
            detail["readOnlyReason"] = readOnlyReason
        }
    }

    nonisolated static func respond(_ action: [String: Any]) -> String {
        switch action["action"] as? String {
        case "native-queue" where (action["nativeQueue"] as? [String: Any])?["queueAction"] as? String == "list":
            return #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[{"type":"text","text":"Run the release checklist."}]},{"id":"q2","clientUserMessageId":"c2","input":[{"type":"text","text":"Then tag the build."}]}],"nextCursor":null}}"#
        case "session-lifecycle-logs":
            return #"{"ok":true,"result":{"sessionId":"demo","state":"running","output":"started\nready"}}"#
        case let name? where name.hasPrefix("session-lifecycle-"):
            return #"{"ok":true,"result":{"sessionId":"demo","state":"running","id":"job-7"}}"#
        case "session-fork":
            return #"{"ok":true,"result":{"session":{"id":"demo-fork"}}}"#
        default:
            return #"{"ok":true,"result":{}}"#
        }
    }
}
#endif
