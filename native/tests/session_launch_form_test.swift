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
        form.folder = ""
        check(!form.isComplete, "a folder is required")

        let notion = SessionLaunchForm(agent: "notion", message: "Draft the outline")
        check(notion.isComplete, "Notion needs no folder")
        check(notion.cwd.isEmpty && notion.modelID.isEmpty, "Notion sends no folder or model")
    }

    private static func checkSwitching() {
        var form = SessionLaunchForm(agent: "claude", folder: "/work/sample", model: "sample-model", effort: "high", message: "Keep me")
        form.switchAgent(to: "codex")
        check(form.folder == "/work/sample" && form.message == "Keep me", "switching agents keeps folder and message")
        check(form.model.isEmpty && form.effort.isEmpty, "switching agents clears model and effort")

        form.effort = "high"
        form.selectModel("other-model")
        check(form.model == "other-model" && form.effort.isEmpty, "changing the model clears effort")
    }

    private static func checkEfforts() {
        let catalog = [
            ModelOption(id: "fast-model", displayName: "Fast", description: nil, default: true, allowsCustom: nil, supportedReasoningEfforts: ["low", "medium"], defaultReasoningEffort: "low"),
            ModelOption(id: "deep-model", displayName: "Deep", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: ["high", "max"], defaultReasoningEffort: "high"),
            ModelOption(id: "plain-model", displayName: "Plain", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil),
        ]
        check(SessionLaunchForm(agent: "codex").efforts(models: catalog) == ["low", "medium"], "the runtime default offers the default model's efforts")
        check(SessionLaunchForm(agent: "claude", model: "deep-model").efforts(models: catalog) == ["high", "max"], "a chosen model offers its own efforts")
        check(SessionLaunchForm(agent: "codex", model: "plain-model").efforts(models: catalog).isEmpty, "a model without listed efforts offers none")
        check(SessionLaunchForm(agent: "claude").efforts(models: []).isEmpty, "no catalog offers no effort")
    }

    private static func checkOptions() throws {
        let json = #"{"surfaces":[{"id":"claude","workspace":true},{"id":"notion","workspace":false}],"workspaces":[],"launchers":[{"id":"tmux","label":"tmux","agents":["claude"],"available":true,"detail":"synthetic"},{"id":"external","label":"external","agents":null,"available":false,"detail":"not configured"}]}"#
        let options = try JSONDecoder().decode(SessionCreationOptions.self, from: Data(json.utf8))
        check(options.surfaces.map(\.id) == ["claude", "notion"], "session options list Notion")
        check(options.launchers?.map(\.agents) == [["claude"], []], "a launcher without agents decodes as serving none")
    }

    private static func checkPayloads() async throws {
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "synthetic", session: CaptureProtocol.session)

        let codex = try await send(SessionLaunchForm(agent: "codex", folder: "/work/sample", model: "sample-model", effort: "high", message: "  Summarize  "), api)
        check(codex["action"] as? String == "session-create" && codex["surface"] as? String == "codex", "Codex uses session-create")
        check(codex["message"] as? String == "Summarize" && codex["cwd"] as? String == "/work/sample", "trimmed message and folder are sent")
        check(codex["model"] as? String == "sample-model" && codex["effort"] as? String == "high", "model and effort are sent")
        check(Set(codex.keys) == ["action", "surface", "message", "cwd", "model", "effort"], "Codex sends only the sheet's fields")

        let claude = try await send(SessionLaunchForm(agent: "claude", folder: "/work/sample", effort: "max", message: "Review"), api)
        check(claude["model"] as? String == "" && claude["effort"] as? String == "max", "the runtime default model sends an empty model with the chosen effort")
        check(Set(claude.keys) == ["action", "surface", "message", "cwd", "model", "effort"], "Claude sends only the sheet's fields")

        let plain = try await send(SessionLaunchForm(agent: "claude", folder: "/work/sample", message: "Review"), api)
        check(Set(plain.keys) == ["action", "surface", "message", "cwd", "model"], "no effort is sent when none is chosen")

        let notion = try await send(SessionLaunchForm(agent: "notion", folder: "/work/sample", model: "ignored", effort: "high", message: "Outline"), api)
        check(notion["action"] as? String == "notion-create", "Notion uses notion-create")
        check(notion["cwd"] as? String == "" && notion["model"] as? String == "" && notion["effort"] == nil, "Notion sends no folder, model or effort")
    }

    private static func send(_ form: SessionLaunchForm, _ api: AgenthailAPI) async throws -> [String: Any] {
        CaptureProtocol.body = nil
        _ = try await api.createSession(body: form.creationBody())
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
