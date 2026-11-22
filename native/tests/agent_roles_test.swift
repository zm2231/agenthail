@testable import AgenthailHelpers
import Foundation

@main
@MainActor
struct AgentRolesTest {
    static let view = #"""
    {"roles":[
      {"name":"worker","builtIn":true,"edited":false,"description":"Any session","reach":"descendants","decisions":{"session-create-remote":"notify","project-archive":"ask"},"definition":{"description":"Any session","reach":"descendants","tools":null},"tools":["agenthail_read","agenthail_send"],"allTools":true,"depth":3,"running":8,"effectiveDefaults":{"session-create-remote":"notify","project-archive":"ask"}},
      {"name":"reviewer","builtIn":false,"edited":false,"description":"","reach":"project","decisions":{"session-create-remote":"notify","project-archive":"ask"},"definition":{"reach":"project","actions":{"project-archive":"ask"},"tools":["agenthail_read"],"depth":2},"tools":["agenthail_read"],"allTools":false,"depth":2,"running":8}
    ],
    "assignments":[{"kind":"session","target":"s-gone","role":"reviewer","stale":"no such session"},{"kind":"alias","target":"release","role":"lead"}],
    "actions":[{"name":"session-create-remote","default":"notify","detail":"Start a session on a paired computer","ownProject":false},{"name":"project-archive","default":"notify","detail":"Archive its own project","ownProject":true}],
    "tools":["agenthail_read","agenthail_send"]}
    """#

    static func main() async throws {
        let decoded = try JSONDecoder().decode(AgentRolesView.self, from: Data(view.utf8))
        let worker = decoded.role("worker")!, reviewer = decoded.role("reviewer")!
        let archive = decoded.actions[1], remote = decoded.actions[0]
        check(worker.definition.tools == nil && reviewer.definition.tools == ["agenthail_read"] && decoded.assignments[0].stale == "no such session" && decoded.assignments[1].stale == nil, "the view decodes tools null, lists and stale reasons")

        check(AgentRoleFacts.effectiveDefault(archive, in: worker) == "ask", "a role's effective default comes from the daemon")
        check(AgentRoleFacts.effectiveDefault(archive, in: reviewer) == "notify", "without effective defaults the action's default stands")

        var draft = AgentRoleDraft(reviewer)
        check(!draft.changed && draft.decision("project-archive") == .ask && draft.decision("session-create-remote") == nil, "a draft starts as the stored role, changed only where it differs")
        draft.setDecision(.notify, for: remote, effectiveDefault: "notify")
        check(!draft.changed && draft.definition.actions?["session-create-remote"] == nil, "choosing the default value stores no change")
        draft.setDecision(.allow, for: remote, effectiveDefault: "notify")
        check(draft.changed && draft.decision("session-create-remote") == .allow, "a different decision is a change")
        draft.setDecision(nil, for: archive, effectiveDefault: "notify")
        check(draft.definition.actions == ["session-create-remote": "allow"], "Default removes the change: \(String(describing: draft.definition.actions))")
        check(draft.depth == 2 && draft.running == AgentRoleFacts.defaultRunning, "limits read their value or the default")
        draft.depth = AgentRoleFacts.defaultDepth
        check(draft.definition.depth == nil, "a limit at its default stores none")
        draft.setTool("agenthail_send", shown: true, every: decoded.tools)
        check(draft.allTools && draft.definition.tools == nil, "showing the last hidden tool is every tool")
        draft.setTool("agenthail_read", shown: false, every: decoded.tools)
        check(draft.definition.tools == ["agenthail_send"] && !draft.sees("agenthail_read"), "hiding a tool lists the rest")
        draft.setAllTools(false, every: decoded.tools)
        check(draft.definition.tools == decoded.tools, "turning off every tool starts from the full list")

        let put = try JSONSerialization.jsonObject(with: JSONEncoder().encode(draft.request)) as! [String: Any]
        let role = put["role"] as! [String: Any]
        check(put["op"] as? String == "put" && put["name"] as? String == "reviewer" && role["reach"] as? String == "project" && role["depth"] == nil && put["kind"] == nil, "a put sends the whole role: \(put)")
        draft.revert()
        check(!draft.changed, "revert returns to the stored role")

        let all = try JSONSerialization.jsonObject(with: JSONEncoder().encode(AgentRoleDraft(worker).request)) as! [String: Any]
        check((all["role"] as! [String: Any])["tools"] is NSNull, "every tool is sent as tools null: \(all)")

        let copy = AgentRoleDraft(name: "reviewer-2", copying: reviewer.definition)
        check(copy.name == "reviewer-2" && copy.definition == reviewer.definition && copy.request.op == "put", "a new role starts from an existing one")

        let assign = try JSONSerialization.jsonObject(with: JSONEncoder().encode(AgentRoleRequest.assign("reviewer", kind: .identity, target: "release-bot"))) as! [String: Any]
        check(assign as NSDictionary == ["op": "assign", "name": "reviewer", "kind": "identity", "target": "release-bot"] as NSDictionary, "assign sends kind and target: \(assign)")
        let unassign = AgentRoleRequest.unassign(decoded.assignments[1])
        check(unassign.op == "unassign" && unassign.name == "lead" && unassign.kind == "alias" && unassign.target == "release", "unassign names the assignment")
        check(AgentRoleFacts.assignmentLine(decoded.assignments[1]) { _ in nil } == "@release" && AgentRoleFacts.assignmentLine(decoded.assignments[0]) { $0 == "s-gone" ? "@old" : nil } == "@old", "assignments read by alias or session name")

        check(AgentRoleFacts.nameHint("", taken: []) == nil && AgentRoleFacts.nameHint("reviewer", taken: ["reviewer"]) != nil && AgentRoleFacts.nameHint("Bad Name", taken: []) != nil && AgentRoleFacts.nameHint("ok-2", taken: []) == nil, "the name hint flags taken and malformed names")

        let role2 = try JSONDecoder().decode(SessionRole.self, from: Data(#"{"name":"reviewer","reach":"project","source":"assigned to this session","replaces":"worker","label":"worker (replaced by reviewer)"}"#.utf8))
        check(role2.label == "worker (replaced by reviewer)" && role2.replaces == "worker", "a session's role decodes its label")

        let event = try JSONDecoder().decode(AgenthailEvent.self, from: Data(#"{"id":4,"type":"settings.updated","timestamp":"t","data":{"source":"agent-roles"}}"#.utf8))
        let other = try JSONDecoder().decode(AgenthailEvent.self, from: Data(#"{"id":5,"type":"settings.updated","timestamp":"t","data":{"source":"notifications"}}"#.utf8))
        let bare = try JSONDecoder().decode(AgenthailEvent.self, from: Data(#"{"id":6,"type":"state.changed","timestamp":"t","data":{"source":"agent-roles"}}"#.utf8))
        check(event.source == "agent-roles" && other.source == "notifications" && bare.source == nil, "a settings event names its source")
        check(AgentRolesRefresh.refetches(type: event.type, source: event.source) && !AgentRolesRefresh.refetches(type: other.type, source: other.source) && !AgentRolesRefresh.refetches(type: bare.type, source: "agent-roles"), "only an agent-roles settings event refetches")

        try await store(decoded)
        print("ok")
    }

    final class StubTransport: AgentRolesTransport, @unchecked Sendable {
        var view: AgentRolesView
        var reads = 0
        var writes: [AgentRoleRequest] = []
        var refusal: String?

        init(_ view: AgentRolesView) { self.view = view }

        func read() async throws -> AgentRolesRead {
            reads += 1
            return AgentRolesRead(view: view, readOnlyReason: nil)
        }

        func write(_ request: AgentRoleRequest) async throws -> AgentRolesView {
            writes.append(request)
            if let refusal { throw AgenthailAPIError.request(400, refusal) }
            view.assignments.append(AgentRoleAssignment(kind: request.kind ?? "", target: request.target ?? "", role: request.name, stale: nil))
            return view
        }
    }

    static func store(_ view: AgentRolesView) async throws {
        let transport = StubTransport(view)
        let store = AgentRolesStore()
        store.attach(transport, hostID: "h1", hostName: "studio")
        store.receive(type: "settings.updated", source: "agent-roles", hostID: "h1")
        await settle()
        check(transport.reads == 0, "nothing refetches before the screen has read once")

        await store.load()
        check(transport.reads == 1 && store.view?.roles.count == 2 && store.editable && store.hostName == "studio", "a load reads the roles")

        transport.view.roles.removeLast()
        store.receive(type: "settings.updated", source: "agent-roles", hostID: "h1")
        await settle()
        check(transport.reads == 2 && store.view?.roles.count == 1, "an agent-roles event refetches: \(transport.reads)")

        store.receive(type: "settings.updated", source: "notifications", hostID: "h1")
        store.receive(type: "settings.updated", source: "agent-roles", hostID: "h2")
        await settle()
        check(transport.reads == 2, "other settings and other computers do not refetch")

        let sent = await store.send(.assign("lead", kind: .alias, target: "ops"))
        check(sent && transport.reads == 2 && store.view?.assignments.last?.target == "ops", "a write takes the answer as the new view without another read")

        transport.refusal = "a role's name is lowercase letters, digits and dashes, starting with a letter, at most 40 long"
        let refused = await store.send(.put("Bad", AgentRoleDefinition(reach: "project")))
        check(!refused && store.saveError == transport.refusal, "a refused put shows the daemon's message: \(store.saveError ?? "nil")")
        store.clearSaveError()
        check(store.saveError == nil, "the error clears")

        store.attach(transport, hostID: "h2", hostName: "laptop")
        check(store.view == nil && store.hostName == "laptop", "moving to another computer drops the old roles")
    }

    static func settle() async {
        for _ in 0..<5 { await Task.yield() }
        try? await Task.sleep(for: .milliseconds(50))
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
