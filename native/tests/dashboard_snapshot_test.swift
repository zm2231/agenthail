import Foundation

@main
struct DashboardSnapshotTest {
    static func snapshot(updatedAt: String, eventCursor: UInt64?, hostEpoch: String, catalogSeq: UInt64, totalSessions: Int) -> DashboardSnapshot {
        DashboardSnapshot(
            updatedAt: updatedAt,
            eventCursor: eventCursor,
            hostEpoch: hostEpoch,
            catalogSeq: catalogSeq,
            daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil),
            surfaces: [],
            sessions: [],
            totalSessions: totalSessions,
            queue: [],
            channels: [],
            relays: [],
            history: [],
            attention: [],
            deliveryProblems: nil,
            codexRecentHours: 5,
            busyDelivery: "queue"
        )
    }

    static func main() {
        let current = snapshot(updatedAt: "2026-07-24T00:00:00Z", eventCursor: 12, hostEpoch: "host-a", catalogSeq: 12, totalSessions: 4)
        let transportOnlyChange = snapshot(updatedAt: "2026-07-24T00:00:30Z", eventCursor: 13, hostEpoch: "host-a", catalogSeq: 13, totalSessions: 4)
        let visibleChange = snapshot(updatedAt: "2026-07-24T00:00:30Z", eventCursor: 13, hostEpoch: "host-a", catalogSeq: 13, totalSessions: 5)
        precondition(current.hasSamePresentation(as: transportOnlyChange))
        precondition(!current.hasSamePresentation(as: visibleChange))
        var problemChange = transportOnlyChange
        problemChange.deliveryProblems = [DeliveryProblem(deliveryId: 7, sessionId: "s1", sourceSessionId: "s2", message: "Run the tests", reason: "target_not_writable", status: nil, at: "2026-10-04 12:00:00")]
        precondition(!current.hasSamePresentation(as: problemChange))

        let child = try! JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"child","surface":"codex","name":"Review","status":"busy","queueCount":0,"open":false,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"compact":true,"model":true,"interrupt":true,"steer":true},"subagent":{"parentId":"root","rootId":"root","depth":2,"nickname":"Euclid","role":"reviewer"}}"#.utf8))
        precondition(child.subagent == SubagentIdentity(parentId: "root", rootId: "root", depth: 2, nickname: "Euclid", role: "reviewer"))
        let claudeParent = try! JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"session_x","surface":"claude","name":"Lead","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"subagents":{"count":3,"working":1}}"#.utf8))
        precondition(claudeParent.subagents == SubagentRollup(count: 3, working: 1) && claudeParent.subagent == nil)
        let link = try! JSONDecoder().decode(ClaudeSubagentLink.self, from: Data(#"{"parentSessionId":"session_x","agentId":"a1","agentType":"Explore","description":"Map the code","toolUseId":"toolu_1","depth":1,"working":true,"lastActive":"2026-10-04T12:00:00Z","transcriptPath":"/tmp/agent-a1.jsonl"}"#.utf8))
        precondition(link.agentType == "Explore" && link.description == "Map the code" && link.working == true && link.depth == 1)

        let codex = try! JSONDecoder().decode(SurfaceState.self, from: Data(#"{"name":"codex","connected":true,"health":"degraded","healthDetail":"bridge closed","capabilities":{"send":true,"stream":false,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false},"runtime":{"name":"Codex Desktop bridge","reachable":false,"durable":false,"problem":"bridge-unavailable","detail":"bridge closed","remediation":"run 'agenthail launch codex'","notes":[{"problem":"standalone-missing","message":"managed terminals need the standalone Codex runtime","remediation":"install the standalone Codex runtime: curl -fsSL https://chatgpt.com/codex/install.sh | sh"}]}}"#.utf8))
        precondition(codex.needsAttention)
        precondition(codex.runtime?.advice.count == 2)
        precondition(codex.runtime?.advice[0].contains("agenthail launch codex") == true)
        precondition(codex.runtime?.advice[1].contains("chatgpt.com/codex/install.sh") == true)
        let healthy = try! JSONDecoder().decode(SurfaceState.self, from: Data(#"{"name":"claude","connected":true,"health":"healthy","capabilities":{"send":true,"stream":false,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}}"#.utf8))
        precondition(!healthy.needsAttention && healthy.runtime == nil)
        precondition(healthy.repairAction == nil && healthy.repairLabel == nil)
        let relays = try! JSONDecoder().decode([RelayState].self, from: Data(#"[{"id":3,"from":"@research","to":"@builder","pattern":"READY","once":true,"active":false,"fireCount":1,"lastFiredAt":"2026-10-05 11:00:00"},{"id":4,"from":"@a","to":"@b","pattern":".*","once":false,"active":true,"fireCount":0}]"#.utf8))
        precondition(relays[0].once && !relays[0].active && relays[0].fireCount == 1 && relays[0].lastFiredAt == "2026-10-05 11:00:00")
        precondition(!relays[1].once && relays[1].active && relays[1].lastFiredAt == nil)
        let outcome = try! JSONDecoder().decode(HistoryState.self, from: Data(#"{"id":1,"createdAt":"2026-10-05 11:00:00","kind":"deliver","evidence":"delivered"}"#.utf8))
        precondition(outcome.evidence == "delivered")
        let settings = try! JSONDecoder().decode(DashboardSettingsState.self, from: Data(#"{"dashboard":{"enabled":true,"listen":"127.0.0.1:7412","codexRecentHours":8,"busyDelivery":"steer","remoteAccess":{"enabled":true,"provider":"tailscale","port":7412}},"remoteAccess":{"enabled":true,"desired":true,"provider":"tailscale","url":"https://mac.example.ts.net:7412/?token=fixture#overview","dnsName":"mac.example.ts.net","port":7412},"notifications":{"enabled":false,"available":true,"authorization":"unknown","authorized":false,"alerts":false,"sounds":false},"daemon":{"pid":1,"running":true}}"#.utf8))
        precondition(settings.dashboard.listen == "127.0.0.1:7412" && settings.dashboard.codexRecentHours == 8 && settings.dashboard.busyDelivery == "steer")
        precondition(settings.remoteAccess.url?.hasPrefix("https://mac.example.ts.net:7412/") == true)
        print("dashboard snapshot tests passed")
    }
}
