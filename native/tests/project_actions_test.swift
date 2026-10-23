@testable import AgenthailHelpers
import Foundation

@main
struct ProjectActionsTest {
    static func main() async {
        let snapshot = try! JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"""
        {"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24,
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

        expect(ProjectMenu.items(for: projects[0]) == [
            .rename,
            .newConversation(checkout: 0, label: "New conversation on laptop"),
            .newConversation(checkout: 1, label: "New conversation on server"),
            .lead(change: true),
            .clearLead,
            .remove(projectID: "repo:web", label: "Remove on laptop"),
        ], "a project on several hosts names each checkout by its host, and only added checkouts can be removed")
        expect(ProjectMenu.items(for: projects[1]) == [
            .rename,
            .newConversation(checkout: 0, label: "New conversation"),
            .lead(change: false),
            .remove(projectID: "folder:notes", label: "Remove"),
        ], "a project on this machine alone names no host")
        expect(ProjectMenu.items(for: projects[2]).isEmpty, "an archived project that was never added offers nothing")
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
