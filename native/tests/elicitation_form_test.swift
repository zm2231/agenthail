@testable import AgenthailHelpers
import Foundation

@main
struct ElicitationFormTest {
    static func main() throws {
        let json = #"""
        {"permissionMode":"","requests":[{"id":"e1","kind":"input","tool":"","alwaysAllow":false,"createdAt":"2026-10-20T00:00:00Z","elicitation":{"server":"deploy","message":"Where should this deploy?","mode":"form","fields":[
          {"name":"env","title":"Environment","description":"","type":"string","required":true,"multiSelect":false,"options":[{"value":"staging","label":"Staging"},{"value":"prod","label":"Production"}]},
          {"name":"replicas","title":"Replicas","description":"","type":"integer","required":false,"multiSelect":false,"default":2},
          {"name":"notify","title":"","description":"","type":"boolean","required":false,"multiSelect":false,"default":true},
          {"name":"regions","title":"Regions","description":"","type":"string","required":false,"multiSelect":true,"options":[{"value":"us","label":"US"},{"value":"eu","label":"EU"}]},
          {"name":"note","title":"","description":"Why now","type":"string","required":false,"multiSelect":false,"default":{"odd":1}}
        ]}},{"id":"e2","kind":"input","tool":"","alwaysAllow":false,"elicitation":{"server":"docs","message":"Sign in","mode":"url","url":"https://example.com/auth"}}]}
        """#
        let state = try JSONDecoder().decode(SessionRequests.self, from: Data(json.utf8))
        guard let form = state.requests.first?.elicitation, let fields = form.fields, fields.count == 5 else {
            fatalError("decoded \(state)")
        }
        check(state.requests[1].elicitation?.mode == "url" && state.requests[1].elicitation?.url == "https://example.com/auth", "link \(state.requests[1])")
        check(fields[4].initial == nil && fields[2].label == "notify", "a default of a type MCP does not define is dropped: \(fields[4])")

        var entered = ElicitationForm()
        check(entered.content(fields) == nil, "a form with a required field empty is not ready")
        check(entered.text(fields[1]) == "2" && entered.isOn(fields[2]), "defaults are shown until changed")
        entered.texts["env"] = "prod"
        check(entered.content(fields) == ["env": .string("prod"), "replicas": .number(2), "notify": .bool(true), "regions": .strings([])], "content \(String(describing: entered.content(fields)))")
        entered.pick("eu", in: fields[3], true)
        entered.texts["replicas"] = "2.5"
        check(entered.content(fields) == nil, "an integer field holding a fraction is not ready")
        entered.texts["replicas"] = "3"
        entered.on["notify"] = false
        entered.texts["note"] = "  release  "
        let content = entered.content(fields)
        check(content == ["env": .string("prod"), "replicas": .number(3), "notify": .bool(false), "regions": .strings(["eu"]), "note": .string("release")], "content \(String(describing: content))")

        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        let encoded = String(data: try encoder.encode(RequestDecision.content(content ?? [:])), encoding: .utf8)
        check(encoded == #"{"behavior":"allow","content":{"env":"prod","note":"release","notify":false,"regions":["eu"],"replicas":3}}"#, "encoded \(encoded ?? "")")
        print("elicitation form ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fatalError(message) }
    }
}
