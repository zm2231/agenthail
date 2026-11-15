@testable import AgenthailHelpers
import Foundation

func check(_ condition: Bool, _ message: String) {
    guard condition else {
        print("FAIL: \(message)")
        exit(1)
    }
}

func project(_ json: String) -> ProjectState {
    try! JSONDecoder().decode(ProjectState.self, from: Data(json.utf8))
}

func list(_ json: String) -> ExtensionsList {
    try! ExtensionDates.decoder().decode(ExtensionsList.self, from: Data(json.utf8))
}

@main
struct ProjectWorkTest {
    static func main() {
        let both = project(#"""
        {"id":"repo:github.com/acme/web","displayName":"web","checkouts":[
          {"host":{"id":"h-server","name":"server","state":"connected"},"projectId":"h-server::repo:github.com/acme/web","rootPath":"/srv/web"},
          {"host":{"id":"h-laptop","name":"laptop","state":"local"},"projectId":"repo:github.com/acme/web","rootPath":"/home/a/web"}],
         "coordinators":[{"host":{"id":"h-server","name":"server","state":"connected"},"name":"lead","surface":"claude"}]}
        """#)
        check(ProjectWork.place(both, preferring: "h-laptop") == .init(hostID: "h-laptop", projectID: "repo:github.com/acme/web"), "this computer's own ledger comes first")

        let remote = project(#"""
        {"id":"h-server::folder:notes","displayName":"notes","checkouts":[
          {"host":{"id":"h-build","name":"build","state":"connected"},"projectId":"h-build::folder:b","rootPath":"/b"},
          {"host":{"id":"h-server","name":"server","state":"connected"},"projectId":"h-server::folder:notes","rootPath":"/srv/notes"}],
         "coordinators":[{"host":{"id":"h-server","name":"server","state":"connected"},"name":"lead","surface":"codex"}]}
        """#)
        check(ProjectWork.place(remote, preferring: "h-laptop") == .init(hostID: "h-server", projectID: "folder:notes"), "the lead's computer comes next, with its own id for the project: \(ProjectWork.place(remote, preferring: "h-laptop"))")

        let leadless = project(#"""
        {"id":"h-build::folder:b","displayName":"b","checkouts":[
          {"host":{"id":"h-build","name":"build","state":"connected"},"projectId":"h-build::folder:b","rootPath":"/b"}]}
        """#)
        check(ProjectWork.place(leadless, preferring: "h-laptop") == .init(hostID: "h-build", projectID: "folder:b"), "then the first checkout's computer")

        let localLead = project(#"""
        {"id":"folder:x","displayName":"x","checkouts":[{"host":{"id":"h-build","name":"build","state":"connected"},"projectId":"h-build::folder:y","rootPath":"/y"}],
         "coordinators":[{"name":"lead","surface":"claude"}]}
        """#)
        check(ProjectWork.place(localLead, preferring: "h-laptop") == .init(hostID: "h-laptop", projectID: "folder:x"), "a lead without a host is on this computer")

        let origin = URL(string: "http://127.0.0.1:7412")!
        check(ProjectWork.page(nil, projectID: "p", origin: origin, hostName: "studio") == .reading, "no list yet reads as reading")

        let served = list(#"{"host":"running","extensions":[],"builtIn":{"ledger":{"port":7480,"entry":"/","published":false}}}"#)
        check(ProjectWork.page(served, projectID: "repo:github.com/acme/a+b&c=d#e", origin: origin, hostName: "studio") == .shown(URL(string: "http://127.0.0.1:7480/?project=repo:github.com/acme/a%2Bb%26c%3Dd%23e")!), "the page opens on its own port with the project escaped: \(ProjectWork.page(served, projectID: "repo:github.com/acme/a+b&c=d#e", origin: origin, hostName: "studio"))")

        let odd = ProjectWork.page(served, projectID: "implicit:/work/a%26b;c d", origin: origin, hostName: "studio")
        check(odd == .shown(URL(string: "http://127.0.0.1:7480/?project=implicit:/work/a%2526b%3Bc%20d")!), "percent, semicolon and space are escaped: \(odd)")
        check(URLComponents(string: "http://127.0.0.1:7480/?project=implicit:/work/a%2526b%3Bc%20d")?.queryItems?.first?.value == "implicit:/work/a%26b;c d", "the escaped id decodes back to the same id")

        let tailnet = URL(string: "https://studio.example.ts.net:7412")!
        check(ProjectWork.page(served, projectID: "p", origin: tailnet, hostName: "studio") == .unavailable("studio doesn't publish this on the tailnet."), "an unpublished page is not opened over https")
        let published = list(#"{"host":"running","extensions":[],"builtIn":{"ledger":{"port":7480,"entry":"/","published":true}}}"#)
        check(ProjectWork.page(published, projectID: "p", origin: tailnet, hostName: "studio") == .shown(URL(string: "https://studio.example.ts.net:7480/?project=p")!), "a published page opens over https")

        let failed = list(#"{"host":"running","extensions":[],"builtIn":{"ledger":{"port":0,"entry":"/","error":"port in use"}}}"#)
        check(ProjectWork.page(failed, projectID: "p", origin: origin, hostName: "studio") == .unavailable("port in use"), "the page's error is shown")

        let off = list(#"{"host":"off","extensions":[],"builtIn":{}}"#)
        check(ProjectWork.page(off, projectID: "p", origin: origin, hostName: "studio") == .unavailable("Extensions are off on studio, and the work list is served with them."), "extensions off says why")
        let older = list(#"{"host":"running","extensions":[]}"#)
        check(ProjectWork.page(older, projectID: "p", origin: origin, hostName: "studio") == .unavailable("studio doesn't serve the work list yet. Update Agenthail there."), "an older computer says to update")
        let starting = list(#"{"host":"running","extensions":[],"builtIn":{}}"#)
        check(ProjectWork.page(starting, projectID: "p", origin: origin, hostName: "studio") == .unavailable("studio is still starting its work list. Try again in a moment."), "a daemon that hasn't served the page yet is starting, not old")

        check(ProjectWork.controllerID(.init(hostID: "h1", projectID: "p")) != ProjectWork.controllerID(.init(hostID: "h1", projectID: "q")), "each project keeps its own page")
        print("project_work_test: PASS")
    }
}
