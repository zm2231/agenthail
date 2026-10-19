import Foundation

@main
struct CreationHostPickerTest {
    static func main() throws {
        let forge = HostHealth(id: "h0123456789ab", name: "forge", state: "connected")
        let lab = HostHealth(id: "h0000000000cd", name: "lab", state: "unreachable")
        let daemon = DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: SessionHost(id: "h00000000000a", name: "mac", state: "local"), hosts: [forge, lab])
        func session(host: SessionHost?) -> SessionState {
            var state = SessionState(id: "s1", surface: "codex", name: "builder", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
            state.host = host
            state.cwd = "/srv/builder"
            return state
        }
        let onForge = session(host: SessionHost(id: forge.id, name: "forge", state: "connected"))
        let onLab = session(host: SessionHost(id: lab.id, name: "lab", state: "unreachable"))
        let onMac = session(host: SessionHost(id: "h00000000000a", name: "mac", state: "local"))

        let unpaired = CreationHostPicker(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: []), selectedSession: onForge)
        expect(!unpaired.isVisible && unpaired.hostQuery == nil, "no paired host hides the button and starts here")

        var picker = CreationHostPicker(daemon: daemon, selectedSession: nil)
        expect(picker.isVisible && picker.hosts.map(\.name) == ["mac", "forge", "lab"], "this machine comes first, then each paired host")
        expect(picker.hostQuery == nil && picker.selectedHost?.isLocal == true, "default is this machine")
        expect(CreationHostPicker(daemon: daemon, selectedSession: onForge).hostQuery == forge.id, "default follows the selected session's host")
        expect(CreationHostPicker(daemon: daemon, selectedSession: onLab).hostQuery == nil, "an unreachable session host falls back to this machine")
        expect(CreationHostPicker(daemon: daemon, selectedSession: onMac).hostQuery == nil, "a local session keeps this machine")

        expect(!picker.select(lab.id) && picker.hostQuery == nil, "a host that is not connected cannot be chosen")
        expect(picker.hosts.first { $0.id == lab.id }?.stateLabel == "Unreachable", "a disabled host shows its state")
        expect(picker.isLoading && !picker.canStart, "nothing starts before the catalog loads")

        let local = try decode(#"{"host":{"id":"h00000000000a","name":"mac","state":"local"},"surfaces":[{"id":"codex","workspace":true,"connected":true}],"workspaces":["/Users/me/app"]}"#)
        picker.receive(options: local, for: picker.generation)
        picker.receive(models: [], agent: "codex", for: picker.generation)
        expect(picker.canStart && picker.folders == ["/Users/me/app"] && picker.models["codex"] != nil, "this machine's catalog is applied")

        let stale = picker.generation
        expect(picker.select(forge.id) && picker.hostQuery == forge.id, "choosing a connected host selects it")
        expect(picker.options == nil && picker.models.isEmpty && picker.isLoading, "choosing a host drops the previous host's catalog")
        picker.receive(options: local, for: stale)
        expect(picker.options == nil, "an answer for the host the user left is dropped")
        expect(!picker.select(forge.id), "choosing the same host again changes nothing")
        expect(picker.owns(onForge) && !picker.owns(onMac), "the selected session belongs to the chosen host")

        picker.fail("forge is unreachable", for: picker.generation)
        expect(picker.loadError == "forge is unreachable" && !picker.isLoading && !picker.canStart, "a load failure blocks start and says why")
        let remote = try decode(#"{"host":{"id":"h0123456789ab","name":"forge","state":"local"},"surfaces":[{"id":"claude","workspace":true,"connected":false}],"workspaces":["/srv/builder"]}"#)
        picker.receive(options: remote, for: picker.generation)
        expect(picker.loadError == nil && picker.canStart && picker.folders == ["/srv/builder"], "the chosen host's catalog replaces the failure")

        picker.update(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: [HostHealth(id: forge.id, name: "forge", state: "unreachable"), lab]))
        expect(!picker.canStart && picker.hostQuery == forge.id, "a chosen host that drops keeps the choice but cannot start")
        let beforeRemoval = picker.generation
        picker.update(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: [lab]))
        expect(picker.hostQuery == nil && picker.options == nil && picker.generation == beforeRemoval + 1, "a chosen host that is unpaired falls back to this machine and reloads")
        picker.receive(options: remote, for: beforeRemoval)
        expect(picker.options == nil, "the removed host's late answer is dropped")
        picker.update(daemon: DaemonState(running: true, pid: 1, stale: nil, refreshError: nil, host: daemon.host, hosts: []))
        expect(!picker.isVisible && picker.hostQuery == nil, "unpairing every host hides the button")

        var form = SessionLaunchForm(agent: "codex", host: nil, folder: "/Users/me/app", model: "gpt-local", effort: "high", message: "build it")
        form.switchHost(to: forge.id, folder: "/srv/builder")
        expect(form.host == forge.id && form.folder == "/srv/builder" && form.model.isEmpty && form.effort.isEmpty && form.message == "build it" && form.agent == "codex", "switching host keeps the message and agent, resets model, effort and folder")
        form.switchAgent(to: "claude")
        expect(form.host == forge.id, "switching agent keeps the host")
        let body = try JSONSerialization.jsonObject(with: form.creationBody()) as? [String: Any]
        expect(body?["host"] as? String == forge.id && body?["action"] as? String == "session-create", "the create names the chosen host")
        var here = SessionLaunchForm(agent: "codex", folder: "/Users/me/app", message: "build it")
        here.host = nil
        let localBody = try JSONSerialization.jsonObject(with: here.creationBody()) as? [String: Any]
        expect(localBody?["host"] == nil, "a create on this machine names no host")

        expect(onForge.remoteHostID == forge.id && !onForge.canCallByVoice && onMac.canCallByVoice && onMac.remoteHostID == nil, "voice is offered only for this machine's sessions")
        expect(onForge.modelCatalogKey == "\(forge.id)/codex" && onMac.modelCatalogKey == "/codex", "model lists are cached per host")
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
