@testable import AgenthailHelpers
import Foundation

final class ProjectSettingStub: URLProtocol {
    nonisolated(unsafe) static var path: String?
    nonisolated(unsafe) static var body: Data?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        ProjectSettingStub.path = request.url?.path
        if let stream = request.httpBodyStream {
            stream.open()
            var data = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable { let n = stream.read(&buffer, maxLength: buffer.count); if n <= 0 { break }; data.append(buffer, count: n) }
            stream.close()
            ProjectSettingStub.body = data
        } else {
            ProjectSettingStub.body = request.httpBody
        }
        let answer = Data(#"{"ok":true,"setting":{"project":"repo:web","name":"mcp","value":"off"}}"#.utf8)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: answer)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@main
struct ProjectActionsTest {
    static func main() async {
        let snapshot = try! JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"""
        {"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"codexRecentHours":24,
         "projects":[
          {"id":"repo:web","displayName":"web","kind":"git","sessionCount":3,"archived":false,
           "checkouts":[
            {"host":{"id":"h-laptop","name":"laptop.tail.ts.net","state":"local"},"projectId":"repo:web","rootPath":"/home/a/web","added":true,"sessionCount":2,"archived":false},
            {"host":{"id":"h-server","name":"server.tail.ts.net","state":"connected"},"projectId":"h-server::repo:web","rootPath":"/srv/web","added":false,"sessionCount":1,"archived":false}],
           "coordinators":[{"host":{"id":"h-laptop","name":"laptop","state":"local"},"name":"lead","surface":"claude","sessionId":"s-lead","changedAt":"2026-10-23T00:00:00Z"}]},
          {"id":"folder:notes","displayName":"notes","kind":"folder","sessionCount":0,"archived":false,
           "checkouts":[{"host":{"id":"h-laptop","name":"laptop","state":"local"},"projectId":"folder:notes","rootPath":"/home/a/notes","added":true,"sessionCount":0,"archived":false}],
           "coordinators":[]},
          {"id":"repo:old","displayName":"old","kind":"git","sessionCount":0,"archived":true,
           "checkouts":[{"host":{"id":"h-laptop","name":"laptop","state":"local"},"projectId":"repo:old","rootPath":"/home/a/old","added":false,"sessionCount":0,"archived":true}],
           "coordinators":[]}]}
        """#.utf8))
        let projects = snapshot.projects ?? []
        expect(projects.count == 3 && projects[0].checkouts.count == 2 && projects[0].leads.first?.name == "lead", "the snapshot carries projects with their checkouts and leads")

        expect(ProjectMenu.items(for: projects[0], toolsSettable: true) == [
            .rename,
            .newConversation(checkout: 0, label: "New conversation on laptop"),
            .newConversation(checkout: 1, label: "New conversation on server"),
            .lead(change: true),
            .clearLead,
            .remove(projectID: "repo:web", label: "Remove on laptop"),
        ], "a project on several hosts names each checkout by its host, and only added checkouts can be removed")
        expect(ProjectMenu.items(for: projects[1], toolsSettable: true) == [
            .rename,
            .newConversation(checkout: 0, label: "New conversation"),
            .lead(change: false),
            .remove(projectID: "folder:notes", label: "Remove"),
        ], "a project on this machine alone names no host")
        expect(ProjectMenu.items(for: projects[2], toolsSettable: true).isEmpty, "an archived project that was never added offers nothing")
        expect(ProjectMenu.hosts(of: projects[0]) == ["server"] && ProjectMenu.hosts(of: projects[1]).isEmpty, "a project header names every other host with a checkout")
        var twins = projects[0]
        twins.checkouts = [
            ProjectState.Checkout(host: SessionHost(id: "h-home", name: "build.home", state: "connected"), projectId: "h-home::repo:web", rootPath: "/srv/web", added: true),
            ProjectState.Checkout(host: SessionHost(id: "h-office", name: "build.office", state: "connected"), projectId: "h-office::repo:web", rootPath: "/srv/web", added: true),
        ]
        expect(ProjectMenu.hosts(of: twins) == ["build.home", "build.office"], "hosts that share a short name keep their full names")
        expect(ProjectMenu.items(for: twins, toolsSettable: true).contains(.remove(projectID: "h-office::repo:web", label: "Remove on build.office")), "menus tell apart hosts that share a short name")
        twins.checkouts = [
            ProjectState.Checkout(host: SessionHost(id: "h-a", name: "build", state: "connected"), projectId: "h-a::repo:web", rootPath: "/srv/web", added: true),
            ProjectState.Checkout(host: SessionHost(id: "h-b", name: "build", state: "connected"), projectId: "h-b::repo:web", rootPath: "/srv/web", added: true),
        ]
        expect(ProjectMenu.hosts(of: twins) == ["build (h-a)", "build (h-b)"], "hosts that share a full name add their id")
        expect(ProjectMenu.items(for: twins, toolsSettable: true).contains(.remove(projectID: "h-b::repo:web", label: "Remove on build (h-b)")), "menus tell apart hosts that share a full name")
        var withTools = projects[1]
        withTools.mcp = "on"
        let toolItems = ProjectMenu.items(for: withTools, toolsSettable: true)
        expect(toolItems.firstIndex(of: .tools(off: false)).map { $0 + 1 == toolItems.firstIndex(of: .remove(projectID: "folder:notes", label: "Remove")) } == true, "a project with the setting offers to keep the tools from terminal sessions, just above Remove: \(toolItems)")
        withTools.mcp = "off"
        expect(ProjectMenu.items(for: withTools, toolsSettable: true).contains(.tools(off: true)) && ProjectMenuItem.toolsLabel(off: true) == "Give Terminal Sessions the Agenthail Tools" && ProjectMenuItem.toolsLabel(off: false) == "Keep the Agenthail Tools from Terminal Sessions", "a project with the tools off offers to give them back")
        expect(!ProjectMenu.items(for: withTools, toolsSettable: false).contains(.tools(off: true)), "a project this client can't set offers no toggle")
        expect(!ProjectMenu.items(for: projects[1], toolsSettable: true).contains { if case .tools = $0 { return true } else { return false } }, "a host that doesn't report the setting offers no toggle")
        expect(ProjectMenu.toolsChanged("web", on: false) == "Terminal sessions in web no longer get the Agenthail tools.", "the result names the project")
        let decoded = try! JSONDecoder().decode(ProjectState.self, from: Data(#"{"id":"repo:web","displayName":"web","checkouts":[],"mcp":"off"}"#.utf8))
        expect(decoded.toolsOff == true, "a snapshot row's mcp decodes")

        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ProjectSettingStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://daemon.invalid")!, token: "t", session: URLSession(configuration: config))
        try? await api.projectSetting(projectID: "repo:web", setting: "mcp", value: "off")
        let body = ProjectSettingStub.body.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: String] }
        expect(ProjectSettingStub.path == "/api/v1/actions" && body == ["action": "project-setting", "projectId": "repo:web", "setting": "mcp", "value": "off"], "the toggle posts the project-setting action: \(String(describing: ProjectSettingStub.path)) \(String(describing: body))")

        expect(ProjectMenu.host(of: projects[0].checkouts[0]) == nil && ProjectMenu.host(of: projects[0].checkouts[1]) == "h-server", "an action on a remote checkout names its host")

        let options = try! JSONDecoder().decode(SessionCreationOptions.self, from: Data(#"""
        {"surfaces":[],"projects":[{"id":"repo:web","displayName":"web","rootPath":"/home/a/web","kind":"git","added":true},{"id":"repo:api","displayName":"api","rootPath":"/home/a/api","kind":"git","added":false}],"folders":["/home/a/scratch","/home/a/api"]}
        """#.utf8))
        expect(ProjectMenu.folderSuggestions(options) == ["/home/a/api", "/home/a/scratch"], "folder suggestions are the repositories not yet added, then recent folders, each once")

        var local = SessionState(id: "s1", surface: "claude", name: "one", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil, cwd: nil)
        local.cwd = "/home/a/web"
        local.project = ProjectIdentity(id: "repo:web", displayName: "web", rootPath: "/home/a/web", kind: "git", added: true)
        expect(!ProjectMenu.canAddFolder(local), "a folder that is already an added project is not offered again")
        local.cwd = "/home/a/web/sub"
        expect(ProjectMenu.canAddFolder(local), "a subfolder of a project can be added as its own")
        expect(ProjectMenu.canMakeLead(local, projects: projects), "a Claude conversation in a project can become its lead")
        local.id = "s-lead"
        expect(!ProjectMenu.canMakeLead(local, projects: projects), "the lead is not offered to become the lead again")
        var remote = local
        remote.id = "s2"
        remote.host = SessionHost(id: "h-server", name: "server", state: "connected")
        expect(ProjectMenu.host(of: remote) == "h-server" && ProjectMenu.canAddFolder(remote), "a remote conversation's folder is added on its host")
        var child = remote
        child.subagent = SubagentIdentity(parentId: "s2", rootId: "s2", depth: 1, nickname: nil, role: nil)
        expect(!ProjectMenu.canMakeLead(child, projects: projects), "a subagent cannot lead")

        let refusal = Data(#"{"error":{"code":"coordinator_exists","message":"/home/a/web already has coordinator @lead; replace it to take over"}}"#.utf8)
        let error = ProjectActionError(status: 409, data: refusal)
        expect(error.code == "coordinator_exists" && error.needsReplace && error.localizedDescription.hasPrefix("/home/a/web already"), "a lead refusal keeps its code and asks to replace")
        expect(!ProjectActionError(status: 400, data: Data(#"{"error":{"code":"folder_not_found","message":"no such folder"}}"#.utf8)).needsReplace, "a missing folder is not a replace question")

        let first = await ProjectChange.run { throw error }
        expect(first == .confirmReplace("/home/a/web already has coordinator @lead; replace it to take over. Replace it?"), "a lead already set asks before it is replaced")
        let missing = await ProjectChange.run { throw ProjectActionError(status: 400, data: Data(#"{"error":{"code":"folder_not_found","message":"no such folder"}}"#.utf8)) }
        expect(missing == .failed("no such folder"), "other refusals say why")
        let set = await ProjectChange.run { "@lead leads web." }
        expect(set == .done("@lead leads web."), "a change that lands says what changed")
        print("ok")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
