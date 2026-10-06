import Foundation

@main
struct SessionLaunchFormTest {
    static func main() async throws {
        checkValidation()
        checkSwitching()
        checkEfforts()
        try checkOptions()
        try await checkPayloads()
        print("PASS")
    }

    private static func checkValidation() {
        var form = SessionLaunchForm(agent: "codex", folder: "/work/sample", message: "  ")
        check(!form.isComplete, "a blank message can't start")
        form.message = "Summarize the sample"
        check(form.isComplete, "a message and folder can start")
        form.alias = "@two words"
        check(form.problem == "Names can't contain spaces, /, or #.", "an invalid name is reported")
        check(!form.isComplete, "an invalid name blocks starting")
        form.alias = "@sample-task"
        check(form.problem == nil && form.handle == "sample-task", "a valid name drops its @")
        form.codex.outputSchema = "[1, 2]"
        check(form.problem == "Output schema must be a JSON object, at most 64 KiB.", "a non-object schema is reported")
        form.codex.outputSchema = #"{"type":"object"}"#
        check(form.problem == nil, "an object schema is accepted")
        form.codex.outputSchema = "{" + String(repeating: " ", count: CodexCreationSettings.schemaLimit) + "}"
        check(form.problem != nil, "an oversized schema is reported")

        let notion = SessionLaunchForm(agent: "notion", message: "Draft the outline")
        check(notion.isComplete, "Notion needs no folder")
        check(notion.cwd.isEmpty && notion.modelID.isEmpty, "Notion sends no folder or model")
    }

    private static func checkSwitching() {
        var form = SessionLaunchForm(agent: "claude", alias: "keep", folder: "/work/sample", model: "sample-model", message: "Keep me", effort: "high", claudeAgent: "reviewer", worktree: "tree", permissionMode: "plan")
        form.switchAgent(to: "codex")
        check(form.alias == "keep" && form.folder == "/work/sample" && form.message == "Keep me", "switching agents keeps name, folder and message")
        check(form.model.isEmpty && form.effort.isEmpty && form.claudeAgent.isEmpty && form.permissionMode.isEmpty && form.worktree.isEmpty, "switching agents clears agent-specific settings")

        form.effort = "high"
        form.mode = .plan
        form.codex.serviceTier = "fast"
        form.selectModel("other-model")
        check(form.effort.isEmpty && form.mode == .plan, "changing the model clears effort only")
        form.effort = "low"
        form.selectLauncher("tmux")
        check(form.launcher == "tmux" && !form.hasOptions, "a terminal launcher has no options")
        check(form.turnSettings.isEmpty && form.codexSettings.isEmpty && form.effort.isEmpty && form.mode == nil, "choosing a terminal launcher clears options")
        form.selectLauncher(nil)
        check(form.hasOptions, "the default launcher has options")
    }

    private static func checkEfforts() {
        let codex = SessionLaunchForm(agent: "codex", model: "")
        let catalog = [
            ModelOption(id: "fast-model", displayName: "Fast", description: nil, default: true, allowsCustom: nil, supportedReasoningEfforts: ["low", "medium", "extreme"], defaultReasoningEffort: "low"),
            ModelOption(id: "plain-model", displayName: "Plain", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil),
        ]
        check(codex.efforts(models: catalog) == ["low", "medium"], "the default model's efforts are offered, limited to values the daemon accepts")
        var plain = codex
        plain.model = "plain-model"
        check(plain.efforts(models: catalog) == SessionLaunchForm.codexEfforts, "a model without listed efforts offers every accepted effort")
        check(SessionLaunchForm(agent: "claude").efforts(models: []) == SessionLaunchForm.claudeEfforts, "Claude without a catalog offers every effort it accepts")
        check(SessionLaunchForm(agent: "claude", model: "fast-model").efforts(models: catalog) == ["low", "medium"], "Claude follows the selected model's efforts")
        check(SessionLaunchForm(agent: "notion").efforts(models: []).isEmpty, "Notion has no effort")
    }

    private static func checkOptions() throws {
        let json = #"{"surfaces":[{"id":"claude","workspace":true},{"id":"notion","workspace":false}],"workspaces":[],"launchers":[{"id":"tmux","label":"tmux","agents":["claude"],"available":true,"detail":"synthetic"},{"id":"external","label":"external","agents":null,"available":false,"detail":"not configured"}]}"#
        let options = try JSONDecoder().decode(SessionCreationOptions.self, from: Data(json.utf8))
        check(options.surfaces.map(\.id) == ["claude", "notion"], "session options list Notion")
        check(options.launchers?.map(\.agents) == [["claude"], []], "a launcher without agents decodes as serving none")
    }

    private static func checkPayloads() async throws {
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "synthetic", session: CaptureProtocol.session)

        var codex = SessionLaunchForm(agent: "codex", alias: "@sample-task", folder: "/work/sample", model: "sample-model", message: "  Summarize  ", effort: "high", mode: .plan)
        codex.codex = CodexCreationSettings(approvalPolicy: "never", serviceTier: "flex", outputSchema: #"{"type":"object"}"#)
        let codexBody = try await send(codex, api)
        check(codexBody["action"] as? String == "session-create" && codexBody["surface"] as? String == "codex", "Codex uses session-create")
        check(codexBody["alias"] as? String == "sample-task" && codexBody["message"] as? String == "Summarize", "name and trimmed message are sent")
        check(codexBody["model"] as? String == "sample-model" && codexBody["cwd"] as? String == "/work/sample", "model and folder are sent")
        check(codexBody["effort"] as? String == "high" && codexBody["mode"] as? String == "plan", "effort and mode are sent")
        check(codexBody["approvalPolicy"] as? String == "never" && codexBody["serviceTier"] as? String == "flex", "approvals and service tier are sent")
        check((codexBody["outputSchema"] as? [String: Any])?["type"] as? String == "object", "the output schema is sent as JSON, not text")
        check(codexBody["launcher"] == nil && codexBody["permissionMode"] == nil, "no launcher or Claude field leaks")

        let claude = SessionLaunchForm(agent: "claude", folder: "/work/sample", message: "Review", effort: "max", claudeAgent: "reviewer", worktree: "review-tree", permissionMode: "acceptEdits")
        let claudeBody = try await send(claude, api)
        check(claudeBody["agent"] as? String == "reviewer" && claudeBody["worktree"] as? String == "review-tree", "named agent and worktree are sent")
        check(claudeBody["permissionMode"] as? String == "acceptEdits" && claudeBody["effort"] as? String == "max", "permission mode and effort are sent")
        check(claudeBody["alias"] == nil && claudeBody["name"] == nil && claudeBody["mode"] == nil && claudeBody["approvalPolicy"] == nil, "no empty name or Codex field is sent")

        let notionBody = try await send(SessionLaunchForm(agent: "notion", alias: "notes", folder: "/work/sample", model: "ignored", message: "Outline"), api)
        check(notionBody["action"] as? String == "notion-create" && notionBody["alias"] as? String == "notes", "Notion uses notion-create with its name")
        check(notionBody["cwd"] as? String == "" && notionBody["model"] as? String == "", "Notion sends no folder or model")

        var terminal = SessionLaunchForm(agent: "codex", alias: "term", folder: "/work/sample", model: "sample-model", message: "Run")
        terminal.selectLauncher("tmux")
        let terminalBody = try await send(terminal, api)
        check(terminalBody["launcher"] as? String == "tmux" && terminalBody["alias"] as? String == "term" && terminalBody["model"] as? String == "sample-model", "terminal launches keep name and model")
        check(Set(terminalBody.keys) == ["action", "surface", "message", "cwd", "model", "alias", "launcher"], "terminal launches send no advanced option")

        do {
            _ = try await api.createSession(surface: "codex", message: "Run", cwd: "/work/sample", model: "", codex: CodexCreationSettings(serviceTier: "fast"), launcher: "tmux")
            check(false, "the API refuses advanced options for a terminal launch")
        } catch {}
    }

    private static func send(_ form: SessionLaunchForm, _ api: AgenthailAPI) async throws -> [String: Any] {
        CaptureProtocol.body = nil
        _ = try await api.createSession(surface: form.agent, message: form.trimmedMessage, cwd: form.cwd, model: form.modelID, alias: form.handle, turnSettings: form.turnSettings, codex: form.codexSettings, claude: form.claudeSettings, launcher: form.launcher)
        guard let data = CaptureProtocol.body, let body = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            check(false, "the request body is JSON")
            return [:]
        }
        return body
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}

final class CaptureProtocol: URLProtocol {
    nonisolated(unsafe) static var body: Data?

    static let session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CaptureProtocol.self]
        return URLSession(configuration: configuration)
    }()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.body = request.httpBody ?? request.httpBodyStream.map(Self.read)
        let response = HTTPURLResponse(url: request.url!, statusCode: 201, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(#"{"ok":true,"sessionId":"synthetic-session"}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    private static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }
}
