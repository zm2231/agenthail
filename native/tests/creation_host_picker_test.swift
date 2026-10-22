@testable import AgenthailHelpers
import Foundation

@main
struct CreationHostPickerTest {
    static func main() throws {
        let vps = HostHealth(id: "h0123456789ab", name: "vps", state: "connected")
        let lab = HostHealth(id: "h0000000000cd", name: "lab", state: "unreachable")
        let daemon = DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: SessionHost(id: "h00000000000a", name: "laptop", state: "local"), hosts: [vps, lab])
        func session(host: SessionHost?) -> SessionState {
            var state = SessionState(id: "s1", surface: "codex", name: "builder", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
            state.host = host
            state.cwd = "/srv/builder"
            return state
        }
        let onVps = session(host: SessionHost(id: vps.id, name: "vps", state: "connected"))
        let onLab = session(host: SessionHost(id: lab.id, name: "lab", state: "unreachable"))
        let onLaptop = session(host: SessionHost(id: "h00000000000a", name: "laptop", state: "local"))

        let unpaired = CreationHostPicker(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: []), selectedSession: onVps)
        expect(!unpaired.isVisible && unpaired.hostQuery == nil, "no paired host hides the button and starts here")

        var picker = CreationHostPicker(daemon: daemon, selectedSession: nil)
        expect(picker.isVisible && picker.hosts.map(\.name) == ["laptop", "vps", "lab"], "this machine comes first, then each paired host")
        expect(picker.hostQuery == nil && picker.selectedHost?.isLocal == true, "default is this machine")
        expect(CreationHostPicker(daemon: daemon, selectedSession: onVps).hostQuery == vps.id, "default follows the selected session's host")
        expect(CreationHostPicker(daemon: daemon, selectedSession: onLab).hostQuery == nil, "an unreachable session host falls back to this machine")
        expect(CreationHostPicker(daemon: daemon, selectedSession: onLaptop).hostQuery == nil, "a local session keeps this machine")

        expect(!picker.select(lab.id) && picker.hostQuery == nil, "a host that is not connected cannot be chosen")
        expect(picker.hosts.first { $0.id == lab.id }?.stateLabel == "Unreachable", "a disabled host shows its state")
        expect(picker.isLoading && !picker.canStart, "nothing starts before the catalog loads")

        let local = try decode(#"{"host":{"id":"h00000000000a","name":"laptop","state":"local"},"surfaces":[{"id":"codex","workspace":true,"connected":true}],"projects":[{"id":"p-app","displayName":"app","rootPath":"/Users/me/app","kind":"git"}]}"#)
        picker.receive(options: local, for: picker.generation)
        picker.receive(models: [], agent: "codex", for: picker.generation)
        expect(picker.canStart && picker.projects.map(\.rootPath) == ["/Users/me/app"] && picker.models["codex"] != nil, "this machine's catalog is applied")

        let rooted = try decode(#"{"surfaces":[{"id":"claude","workspace":true}],"projects":[],"claudeConfigRoots":[{"name":"default","root":"/home/me/.claude","primary":true,"signedInAs":{"email":"me@example.com"}},{"name":"work","root":"/home/me/.work/.claude","primary":false,"signedInAs":{"email":"work@example.com"}}]}"#)
        picker.receive(options: rooted, for: picker.generation)
        expect(picker.claudeConfigRoots.allSatisfy { $0.usage == nil } && picker.canStart, "the roots are shown before any usage arrives")
        let resets = ISO8601DateFormatter().string(from: Date().addingTimeInterval(3600))
        let usage = try JSONDecoder().decode(ClaudeRootUsage.self, from: Data(#"{"root":"/home/me/.work/.claude","usage":{"limits":[{"kind":"session","percent":100,"resetsAt":"\#(resets)"}]}}"#.utf8))
        picker.receive(usage: usage, for: picker.generation)
        expect(picker.claudeConfigRoots[0].usage == nil && picker.claudeConfigRoots[1].exhaustedUntil() != nil, "a root's usage fills in that root alone")
        let late = try JSONDecoder().decode(ClaudeRootUsage.self, from: Data(#"{"root":"/home/me/.claude","usage":{"limits":[{"kind":"session","percent":100,"resetsAt":"\#(resets)"}]}}"#.utf8))
        picker.receive(usage: late, for: picker.generation - 1)
        expect(picker.claudeConfigRoots[0].usage == nil, "usage read for an earlier load is dropped")
        picker.receive(options: local, for: picker.generation)

        let stale = picker.generation
        expect(picker.select(vps.id) && picker.hostQuery == vps.id, "choosing a connected host selects it")
        expect(picker.options == nil && picker.models.isEmpty && picker.isLoading, "choosing a host drops the previous host's catalog")
        picker.receive(options: local, for: stale)
        expect(picker.options == nil, "an answer for the host the user left is dropped")
        expect(!picker.select(vps.id), "choosing the same host again changes nothing")
        expect(picker.owns(onVps) && !picker.owns(onLaptop), "the selected session belongs to the chosen host")

        picker.fail("vps is unreachable", for: picker.generation)
        expect(picker.loadError == "vps is unreachable" && !picker.isLoading && !picker.canStart, "a load failure blocks start and says why")
        let remote = try decode(#"{"host":{"id":"h0123456789ab","name":"vps","state":"local"},"surfaces":[{"id":"claude","workspace":true,"connected":false}],"projects":[{"id":"p-builder","displayName":"builder","rootPath":"/srv/builder","kind":"git"}]}"#)
        picker.receive(options: remote, for: picker.generation)
        expect(picker.loadError == nil && picker.canStart && picker.projects.map(\.rootPath) == ["/srv/builder"], "the chosen host's catalog replaces the failure")
        expect(picker.defaultFolder(selected: onLaptop) == "/srv/builder", "a remote host with no selected session there starts in its most recent project")
        var inWorktree = onVps
        inWorktree.cwd = "/srv/builder/.worktrees/fix"
        expect(picker.defaultFolder(selected: inWorktree) == "/srv/builder/.worktrees/fix", "a selected session on the chosen host keeps its own folder")

        picker.update(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: [HostHealth(id: vps.id, name: "vps", state: "unreachable"), lab]))
        expect(!picker.canStart && picker.hostQuery == vps.id, "a chosen host that drops keeps the choice but cannot start")
        let beforeRemoval = picker.generation
        picker.update(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: [lab]))
        expect(picker.hostQuery == nil && picker.options == nil && picker.generation == beforeRemoval + 1, "a chosen host that is unpaired falls back to this machine and reloads")
        picker.receive(options: remote, for: beforeRemoval)
        expect(picker.options == nil, "the removed host's late answer is dropped")
        picker.update(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: []))
        expect(!picker.isVisible && picker.hostQuery == nil, "unpairing every host hides the button")

        var form = SessionLaunchForm(agent: "codex", host: nil, folder: "/Users/me/app", model: "gpt-local", effort: "high", message: "build it")
        form.switchHost(to: vps.id, folder: "/srv/builder")
        expect(form.host == vps.id && form.folder == "/srv/builder" && form.model.isEmpty && form.effort.isEmpty && form.message == "build it" && form.agent == "codex", "switching host keeps the message and agent, resets model, effort and folder")
        form.switchAgent(to: "claude")
        expect(form.host == vps.id, "switching agent keeps the host")
        let body = try JSONSerialization.jsonObject(with: form.creationBody()) as? [String: Any]
        expect(body?["host"] as? String == vps.id && body?["action"] as? String == "session-create", "the create names the chosen host")
        var here = SessionLaunchForm(agent: "codex", folder: "/Users/me/app", message: "build it")
        here.host = nil
        let localBody = try JSONSerialization.jsonObject(with: here.creationBody()) as? [String: Any]
        expect(localBody?["host"] == nil, "a create on this machine names no host")

        expect(onVps.remoteHostID == vps.id && !onVps.canCallByVoice && onLaptop.canCallByVoice && onLaptop.remoteHostID == nil, "voice is offered only for this machine's sessions")
        expect(onVps.modelCatalogKey == "\(vps.id)/codex" && onLaptop.modelCatalogKey == "/codex", "model lists are cached per host")
        expect(daemon.pairedHost(owning: "\(vps.id)::s9")?.name == "vps" && daemon.pairedHost(owning: "s9") == nil && daemon.pairedHost(owning: "h0000000000ff::s9") == nil, "an unlisted session takes the paired host its id names")
        print("creation host picker tests passed")
    }

    static func decode(_ json: String) throws -> SessionCreationOptions {
        try JSONDecoder().decode(SessionCreationOptions.self, from: Data(json.utf8))
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
