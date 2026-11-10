import XCTest
@testable import Agenthail

final class WorkflowParityTests: XCTestCase {
    @MainActor
    func testCodexCreationEncodesEffortAndPlanWithoutLeakingToClaude() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let settings = LaunchSettings(effort: "high", mode: .plan)
        let created = await model.createSession(surface: "codex", message: "Plan the fix", cwd: "/project", model: "chosen", launch: settings)
        XCTAssertTrue(created)
        let action = ParityProtocol.state.actions[0]
        XCTAssertEqual(action["action"] as? String, "session-create")
        XCTAssertEqual(action["model"] as? String, "chosen")
        XCTAssertEqual(action["effort"] as? String, "high")
        XCTAssertEqual(action["mode"] as? String, "plan")
        XCTAssertNil(action["turnSettings"])
        XCTAssertNil(action["outputSchema"])
        XCTAssertNil(action["serviceTier"])
        _ = await model.createSession(surface: "claude", message: "Build", cwd: "/project", model: "", launch: settings)
        XCTAssertNil(ParityProtocol.state.actions[1]["effort"])
        XCTAssertNil(ParityProtocol.state.actions[1]["mode"])
    }

    @MainActor
    func testClaudeCreationPreservesSettingsAndNativeIdentity() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let settings = ClaudeCreationSettings(name: "phone-task", worktree: "phone-work", agent: "reviewer", effort: "high", permissionMode: "plan")
        let created = await model.createSession(surface: "claude", message: "Build", cwd: "/project", model: "sonnet", claude: settings)
        XCTAssertTrue(created)
        XCTAssertEqual(model.requestedSessionID, "computer::native-claude")
        XCTAssertEqual(model.deliveryStatus["computer::native-claude"], "Session started. Waiting for activity.")
        let sent = ParityProtocol.state.actions[0]
        for (key,value) in settings.fields { XCTAssertEqual(sent[key] as? String, value) }
        XCTAssertNil(sent["approvalPolicy"])
        _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "", claude: settings)
        for key in settings.fields.keys { XCTAssertNil(ParityProtocol.state.actions[1][key], "Claude settings must not leak after switching runtime") }
    }

    @MainActor
    func testClaudeUnknownCreationWithoutIdentityDoesNotNavigateOrRetry() async throws {
        ParityProtocol.state.reset(unknown: true)
        let model = makeModel()
        let created = await model.createSession(surface: "claude", message: "Build", cwd: "/project", model: "")
        XCTAssertFalse(created); XCTAssertNil(model.requestedSessionID)
        XCTAssertEqual(model.creationError, SessionCreationReceipt.unknownOutcomeMessage)
        XCTAssertEqual(ParityProtocol.state.actions.count, 1)
    }

    @MainActor
    func testOpeningNewSessionFormClearsPriorAcceptedWarningWithoutRetrying() async {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.creationWarning = "The launcher accepted the request; the conversation is not available yet."
        model.prepareNewSessionForm()
        XCTAssertNil(model.creationWarning)
        XCTAssertNil(model.creationError)
        XCTAssertTrue(ParityProtocol.state.actions.isEmpty)
        let created = await model.createSession(surface: "codex", message: "New launch", cwd: "/project", model: "chosen")
        XCTAssertTrue(created)
        XCTAssertEqual(ParityProtocol.state.actions.count, 1)
    }

    @MainActor
    func testAcceptedTerminalCreationShowsWarningWithoutConversationClaimOrRetry() async throws {
        ParityProtocol.state.reset(accepted: true)
        let model = makeModel()
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen", launcher: LauncherOption(id: "tmux"))
        XCTAssertFalse(created)
        XCTAssertNil(model.requestedSessionID)
        XCTAssertEqual(model.creationWarning, "Submitted to tmux.")
        XCTAssertEqual(ParityProtocol.state.actions.count, 1)
    }

    @MainActor
    func testTerminalLauncherBodyOmitsAdvancedDefaultsAndRejectsUnsupportedSettings() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen", launcher: LauncherOption(id: "tmux"))
        XCTAssertTrue(created)
        XCTAssertEqual(ParityProtocol.state.actions[0]["launcher"] as? String, "tmux")
        XCTAssertNil(ParityProtocol.state.actions[0]["effort"])
        XCTAssertNil(ParityProtocol.state.actions[0]["mode"])

        let settings = LaunchSettings(effort: "high", mode: .plan)
        let rejected = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen", launch: settings, launcher: LauncherOption(id: "tmux"))
        XCTAssertFalse(rejected)
        XCTAssertEqual(ParityProtocol.state.actions.count, 1)
        XCTAssertTrue(model.creationError?.contains("do not support advanced") == true)
    }

    @MainActor
    func testCmuxLauncherUsesTerminalContractForAdvancedSettings() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen", launcher: LauncherOption(id: "cmux"))
        XCTAssertTrue(created)
        XCTAssertEqual(ParityProtocol.state.actions[0]["launcher"] as? String, "cmux")
        XCTAssertNil(ParityProtocol.state.actions[0]["effort"])
        XCTAssertNil(ParityProtocol.state.actions[0]["mode"])

        let settings = LaunchSettings(effort: "high", mode: .plan)
        let rejected = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen", launch: settings, launcher: LauncherOption(id: "cmux"))
        XCTAssertFalse(rejected)
        XCTAssertEqual(ParityProtocol.state.actions.count, 1)
        XCTAssertTrue(model.creationError?.contains("do not support advanced") == true)
    }

    func testQueueDecodesPerTurnOptions() throws {
        let data = Data(#"{"id":7,"sessionId":"demo","sourceSessionId":"sender","target":"demo","message":"next","status":"pending","evidence":"queued","attempts":0,"queuedAt":"now","serviceTier":"fast","outputSchema":{"type":"object","required":["answer"],"additionalProperties":false}}"#.utf8)
        let queue = try JSONDecoder().decode(QueueState.self, from: data)
        XCTAssertEqual(queue.sourceSessionId, "sender")
        XCTAssertEqual(queue.serviceTier, "fast")
        let schema = try JSONSerialization.jsonObject(with: Data(queue.outputSchema!.formatted.utf8)) as! [String:Any]
        XCTAssertEqual(schema["required"] as? [String], ["answer"])
        XCTAssertEqual(schema["additionalProperties"] as? Bool, false)
    }

    func testOnlyFinishedQueueEntriesMoveToHistory() throws {
        func entry(_ status: String) throws -> QueueState {
            try JSONDecoder().decode(QueueState.self, from: Data(#"{"id":9,"sessionId":"demo","target":"demo","message":"m","status":"\#(status)","evidence":"queued","attempts":0,"queuedAt":"now"}"#.utf8))
        }
        for status in ["delivered", "canceled"] { XCTAssertTrue(try entry(status).isHistorical, status) }
        for status in ["pending", "inflight", "dead"] { XCTAssertFalse(try entry(status).isHistorical, status) }
    }

    func testQueuePreservesHistoricalUnknownOutcome() throws {
        let data = Data(#"{"id":8,"sessionId":"demo","target":"demo","message":"uncertain","status":"dead","evidence":"unknown","attempts":1,"queuedAt":"now","historical":true}"#.utf8)
        let queue = try JSONDecoder().decode(QueueState.self, from: data)
        XCTAssertTrue(queue.isHistorical)
        XCTAssertEqual(queue.evidence, "unknown")
    }

    func testCompactGroupingPreservesOrderErrorsAndStableAnchor() throws {
        let data = #"[{"id":"u","kind":"message","role":"user","title":"You","text":"work","truncated":false},{"id":"c","kind":"toolCall","title":"Bash","text":"{\"cmd\":\"swift test\"}","callId":"a","truncated":false},{"id":"r","kind":"toolResult","title":"Result","text":"failed","status":"error","callId":"a","truncated":true},{"id":"m","kind":"message","role":"assistant","title":"Assistant","text":"result","truncated":false}]"#
        let items = try JSONDecoder().decode([TimelineItem].self, from: Data(data.utf8))
        let groups = TimelineGroup.make(items)
        XCTAssertEqual(groups.count, 3)
        XCTAssertEqual(groups.flatMap(\.items).map(\.id), items.map(\.id))
        XCTAssertEqual(groups[1].callCount, 1); XCTAssertEqual(groups[1].errorCount, 1)
        XCTAssertEqual(ToolPresentation(name: items[1].title, text: items[1].text).summary, "swift test")
        XCTAssertEqual(TimelineGroup.make(Array(items.prefix(2)))[1].id, groups[1].id)
        let separated = [items[1], items[3], items[2]]
        XCTAssertEqual(TimelineGroup.make(separated).count, 3, "Pairing must not move a result across an assistant message")
    }

    func testGroupIDsNeverMeetAProviderID() {
        func item(_ id: String, role: String = "assistant", agent: AgentMessage? = nil) -> TimelineItem {
            var value = TimelineItem(id: id, kind: "message", role: role, title: role, text: id, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
            value.agent = agent
            return value
        }
        let peer = AgentMessage(direction: "in", kind: "peer", peer: "builder", summary: "Branch is ready")
        let groups = TimelineGroup.make([item("p", role: "peer", agent: peer), item("q", role: "peer", agent: peer), item("agents:p")])
        XCTAssertEqual(groups.count, 2)
        XCTAssertEqual(Set(groups.map(\.id)).count, groups.count, "a fold and a reply named like it keep their own ids: \(groups.map(\.id))")
    }

    @MainActor
    func testARunAndItsFirstToolRememberTheirOwnState() throws {
        let calls = (1...7).map { TimelineItem(id: "c\($0)", kind: "toolCall", role: "assistant", title: "Bash", text: "{}", timestamp: nil, callId: "k\($0)", status: nil, truncated: false, truncationReason: nil, bodyRef: nil) }
        let run = try XCTUnwrap(TimelineGroup.make(calls).first)
        XCTAssertTrue(run.folds)
        let memory = TranscriptRowMemory()
        memory.set(run.id, open: true, in: "s")
        XCTAssertNil(memory.isOpen(TranscriptRowID.item(calls[0].id), in: "s"), "opening the run leaves its first tool as it was")
    }

    @MainActor
    func testCreationNavigatesKnownSessionAndPreservesUnknownReceipt() async throws {
        ParityProtocol.state.reset(unknown: true)
        let model = makeModel()
        let options = try await model.sessionOptions(host: nil)
        XCTAssertEqual(options.surfaces.first?.id, "codex")
        XCTAssertEqual(options.projects.map(\.rootPath), ["/project"])
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
        XCTAssertTrue(created)
        XCTAssertEqual(model.requestedSessionID, "computer::created")
        XCTAssertEqual(model.deliveryStatus["computer::created"], "Submitted to codex.")
        XCTAssertEqual(ParityProtocol.state.actions.first?["cwd"] as? String, "/project")
        XCTAssertEqual(ParityProtocol.state.actions.first?["model"] as? String, "chosen")
        model.creatingSession = true
        let duplicate = await model.createSession(surface: "codex", message: "Build", cwd: "", model: "")
        XCTAssertFalse(duplicate); XCTAssertEqual(ParityProtocol.state.actions.count, 1)
    }

    @MainActor
    func testFailedCreationReceiptsStayErrorsUnlessRequested() async throws {
        ParityProtocol.state.reset(failReceipt: true)
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([ParityProtocol.self]))
        do {
            _ = try await api.createSession(surface: "codex", message: "Build", cwd: "/project", model: "")
            XCTFail("a failure receipt is an error unless requested")
        } catch {}
        let receipt = try await api.createSession(surface: "codex", message: "Build", cwd: "/project", model: "", failureReceipts: true)
        XCTAssertEqual(receipt.error?.code, "create_failed")
        XCTAssertEqual(receipt.error?.message, "launcher busy")
    }

    @MainActor
    func testAnUnknownCreateKeepsItsKeyAndADefiniteFailureReleasesIt() async throws {
        let unknown = SessionCreationReceipt.unknownOutcomeMessage
        let cases: [(reply: (Int, String), error: String, sameKey: Bool)] = [
            ((502, #"{"error":{"code":"create_unknown","message":"no answer"}}"#), unknown, true),
            ((504, #"{"error":{"code":"host_outcome_unknown","message":"the paired host never answered"}}"#), unknown, true),
            ((502, #"{"error":{"code":"create_failed","message":"launcher refused"}}"#), "launcher refused", false),
            ((502, #"{"error":{"code":"initial_turn_failed","message":"first turn rejected"},"session":{"id":"s1","surface":"codex","name":"","status":"idle","lastActive":"2026-10-23T00:00:00Z"}}"#), "first turn rejected", true),
        ]
        for entry in cases {
            ParityProtocol.state.reset(create: entry.reply)
            let model = makeModel()
            let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
            XCTAssertFalse(created, entry.reply.1)
            XCTAssertEqual(model.creationError, entry.error)
            XCTAssertNil(model.requestedSessionID)
            _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
            let keys = ParityProtocol.state.actionIdempotencyKeys
            XCTAssertEqual(keys.count, 2)
            XCTAssertEqual(keys[0] == keys[1], entry.sameKey, entry.reply.1)
        }
    }

    @MainActor
    func testStartAgainAfterAnUnknownOrHaltedCreateSendsANewKey() async throws {
        let cases: [(reply: (Int, String), offers: Bool)] = [
            ((502, #"{"error":{"code":"create_unknown","message":"no answer"}}"#), true),
            ((502, #"{"error":{"code":"initial_turn_failed","message":"first turn rejected"},"session":{"id":"s1","surface":"codex","name":"","status":"idle","lastActive":"2026-10-23T00:00:00Z"}}"#), true),
            ((502, #"{"error":{"code":"create_failed","message":"launcher refused"}}"#), false),
        ]
        for entry in cases {
            ParityProtocol.state.reset(create: entry.reply)
            let model = makeModel()
            _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
            XCTAssertEqual(model.creationCanStartAgain, entry.offers, entry.reply.1)
            guard entry.offers else { continue }
            model.startCreationAgain()
            XCTAssertFalse(model.creationCanStartAgain)
            XCTAssertNil(model.creationError)
            _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
            XCTAssertTrue(model.creationCanStartAgain, entry.reply.1)
            _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
            let keys = ParityProtocol.state.actionIdempotencyKeys
            XCTAssertEqual(keys.count, 3)
            XCTAssertNotEqual(keys[0], keys[1], entry.reply.1)
            XCTAssertEqual(keys[1], keys[2], entry.reply.1)
        }
    }

    @MainActor
    func testANewSessionFormForgetsStartAgain() async throws {
        ParityProtocol.state.reset(create: (502, #"{"error":{"code":"create_unknown","message":"no answer"}}"#))
        let model = makeModel()
        _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
        XCTAssertTrue(model.creationCanStartAgain)
        model.prepareNewSessionForm()
        XCTAssertFalse(model.creationCanStartAgain)
        XCTAssertNil(model.creationError)
    }

    @MainActor
    func testAnAcceptedLaunchShowsWhatTheLauncherDidNotKeep() async throws {
        let warning = "Submitted to cmux. Its name @x wasn't kept; name it once it appears in the sidebar."
        ParityProtocol.state.reset(create: (202, #"{"ok":true,"status":"submitted","accepted":true,"retryable":false,"launcher":"cmux","warning":"\#(warning)"}"#))
        let model = makeModel()
        let created = await model.createSession(surface: "claude", message: "Build", cwd: "/project", model: "")
        XCTAssertFalse(created)
        XCTAssertNil(model.creationError)
        XCTAssertEqual(model.creationWarning, warning)
    }

    @MainActor
    func testCreationRetryReplaysSameLogicalRequestButEditedFormGetsNewKey() async throws {
        ParityProtocol.state.reset(fail: true)
        let model = makeModel()
        let firstAttempt = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
        XCTAssertFalse(firstAttempt)
        XCTAssertEqual(model.creationError, "Couldn’t complete request. Your session form is still ready.")
        let sameAttempt = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
        XCTAssertFalse(sameAttempt)
        let editedAttempt = await model.createSession(surface: "codex", message: "Build the edited plan", cwd: "/project", model: "chosen")
        XCTAssertFalse(editedAttempt)
        let keys = ParityProtocol.state.actionIdempotencyKeys
        XCTAssertEqual(keys.count, 3)
        XCTAssertEqual(keys[0], keys[1])
        XCTAssertNotEqual(keys[1], keys[2])
    }

    @MainActor
    func testCreationFailureKeepsRouteAndNotionUsesExistingContract() async throws {
        ParityProtocol.state.reset(fail: true)
        let model = makeModel()
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "", model: "")
        XCTAssertFalse(created); XCTAssertNil(model.requestedSessionID); XCTAssertNotNil(model.creationError)
        XCTAssertFalse(model.creatingSession)
        ParityProtocol.state.reset()
        let notion = await model.createSession(surface: "notion", message: "Research", cwd: "", model: "")
        XCTAssertTrue(notion)
        XCTAssertEqual(ParityProtocol.state.actions.first?["action"] as? String, "notion-create")
    }

    @MainActor
    func testGoalAliasAndQueueSendExactOperations() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        var detail = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.utf8))
        detail.session.id = "computer::demo"
        model.selectedDetail = detail
        try await model.editSession(id: "computer::demo", action: "goal-set", text: "Verify release")
        try await model.editSession(id: "computer::demo", action: "alias", text: "release")
        var queue = try JSONDecoder().decode(QueueState.self, from: Data(#"{"id":7,"sessionId":"demo","target":"demo","message":"next","status":"pending","evidence":"queued","attempts":0,"queuedAt":"now"}"#.utf8))
        queue.hostID = "computer"
        try await model.cancelQueue(queue)
        let actions = ParityProtocol.state.actions
        XCTAssertEqual(actions.compactMap { $0["action"] as? String }, ["goal-set","alias","queue-cancel"])
        XCTAssertEqual(actions[0]["message"] as? String, "Verify release")
        XCTAssertEqual(actions[1]["alias"] as? String, "release")
        XCTAssertEqual(actions[2]["queueId"] as? Int, 7)
        XCTAssertTrue(model.pendingControls.isEmpty)
    }

    @MainActor
    func testTakingBackAQueuedMessageFollowsItsSessionAcrossARekey() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[{"id":"old","surface":"claude","name":"task","status":"working","queueCount":1,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}],"totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#.utf8)))
        model.host(try XCTUnwrap(model.connection("computer")), rekeyed: ["old": "new"])
        func queued(_ id: Int, _ message: String) throws -> QueueState {
            var item = try JSONDecoder().decode(QueueState.self, from: Data(#"{"id":\#(id),"sessionId":"old","target":"old","message":"\#(message)","status":"pending","evidence":"","attempts":0,"queuedAt":"now"}"#.utf8))
            item.hostID = "computer"
            return item
        }
        try await model.takeBack(queued(9, "first"))
        await model.loadSession("computer::old")
        XCTAssertEqual(model.selectedSessionID, "computer::new")
        XCTAssertEqual(model.composer, "first")
        try await model.takeBack(queued(10, "second"))
        XCTAssertEqual(model.composer, "second\n\nfirst")
        XCTAssertEqual(ParityProtocol.state.actions.compactMap { $0["action"] as? String }, ["queue-cancel", "queue-cancel"])
        XCTAssertEqual(ParityProtocol.state.actions.compactMap { $0["queueId"] as? Int }, [9, 10])
    }

    @MainActor
    func testAStashedDraftComesBackAfterSendingAndFollowsARekey() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(Self.codexSessionSnapshot.utf8)))
        await model.loadSession("computer::old")
        var detail = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.utf8))
        detail.session.id = "computer::old"
        model.selectedDetail = detail
        model.composer = "half written"
        model.toggleStash()
        XCTAssertEqual(model.composer, "")
        XCTAssertEqual(model.stashes["computer::old"]?.text, "half written")
        model.composer = "a quick question"
        model.toggleStash()
        XCTAssertEqual(model.composer, "half written")
        XCTAssertEqual(model.stashes["computer::old"]?.text, "a quick question")
        model.send(to: try XCTUnwrap(model.snapshot?.sessions.first))
        XCTAssertEqual(model.composer, "a quick question")
        XCTAssertNil(model.stashes["computer::old"])
        for _ in 0 ..< 200 where !ParityProtocol.state.actions.contains(where: { $0["action"] as? String == "send" }) { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertEqual(ParityProtocol.state.actions.last?["message"] as? String, "half written")
        model.toggleStash()
        model.host(try XCTUnwrap(model.connection("computer")), rekeyed: ["old": "new"])
        XCTAssertNil(model.stashes["computer::old"])
        XCTAssertEqual(model.stashes["computer::new"]?.text, "a quick question")
    }

    @MainActor
    func testRecentPromptsAndForkingFromAPrompt() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(Self.codexSessionSnapshot.utf8)))
        let found = await model.recentPrompts(sessionID: "computer::old", query: "sec")
        XCTAssertEqual(found.prompts.map(\.text), ["second", "first"])
        XCTAssertEqual(found.prompts.map(\.sessionId), ["computer::old", "computer::old"])
        XCTAssertTrue(ParityProtocol.state.promptQueries.first?.contains("sessionId=old") == true)
        XCTAssertTrue(ParityProtocol.state.promptQueries.first?.contains("q=sec") == true)
        let session = try XCTUnwrap(model.snapshot?.sessions.first)
        XCTAssertTrue(SessionOperationAvailability.fork(session))
        try await model.fork(session, before: found.prompts[0])
        let fork = try XCTUnwrap(ParityProtocol.state.actions.last)
        XCTAssertEqual(fork["action"] as? String, "session-fork")
        XCTAssertEqual(fork["sessionId"] as? String, "old")
        XCTAssertEqual((fork["fork"] as? [String: Any])?["beforeTurnId"] as? String, "t2")
        XCTAssertEqual(model.requestedSessionID, "computer::forked")
        await model.loadSession("computer::forked")
        XCTAssertEqual(model.composer, "second")
    }

    @MainActor
    func testMovingASessionAsksTheComputerThatRunsItAndResendsWithConsent() async throws {
        ParityProtocol.state.reset()
        ParityProtocol.state.answerMoves([
            (409, #"{"error":{"code":"move_needs_consent","message":"a terminal holds it"}}"#),
            (200, #"{"ok":true,"result":{"move":{"id":"m1"},"host":{"id":"hpeer","name":"peer","state":"connected"},"stopped":"terminal process 41","mcpServersWithoutCredentials":["linear"]}}"#),
        ])
        let model = makeModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data("""
        {"updatedAt":"now","daemon":{"running":true,"host":{"id":"hself","name":"laptop","state":"local"},"hosts":[{"id":"hpeer","name":"peer","state":"connected"},{"id":"hdown","name":"spare","state":"unreachable"}]},"surfaces":[],"sessions":[
          {"id":"old","surface":"claude","name":"task","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}},
          {"id":"m2","surface":"claude","name":"remote","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"host":{"id":"hpeer","name":"peer","state":"connected"}},
          {"id":"m3","surface":"claude","name":"moving","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"move":{"state":"copying","host":"hpeer","hostName":"peer"}}],
         "totalSessions":3,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}
        """.utf8)))
        let sessions = try XCTUnwrap(model.snapshot?.sessions)
        let local = try XCTUnwrap(sessions.first { $0.name == "task" })
        let remote = try XCTUnwrap(sessions.first { $0.name == "remote" })
        let moving = try XCTUnwrap(sessions.first { $0.name == "moving" })
        XCTAssertTrue(model.canMove(local))
        XCTAssertFalse(model.canMove(remote), "a paired host's session moves from that host")
        XCTAssertFalse(model.canMove(moving), "a session already moving cannot move again")
        XCTAssertEqual(moving.move?.label, "Moving to peer: copying")
        let targets = model.moveTargets(local)
        XCTAssertEqual(targets.map(\.name), ["peer", "spare"])
        XCTAssertEqual(targets.map(\.isAvailable), [true, false])

        model.move(local, to: targets[0])
        XCTAssertFalse(model.canMove(local), "a move in flight cannot be sent twice")
        for _ in 0..<300 where model.moveNotices.current == nil { try await Task.sleep(for: .milliseconds(10)) }
        let consent = try XCTUnwrap(model.moveNotices.current)
        XCTAssertEqual(consent.title, "Stop the terminal and move?")
        XCTAssertEqual(ParityProtocol.state.moves.first?["sessionId"] as? String, "old")
        XCTAssertEqual(ParityProtocol.state.moves.first?["host"] as? String, "hpeer")
        XCTAssertEqual(ParityProtocol.state.moves.first?["stopTerminal"] as? Bool, false)

        model.moveNotices.dismiss(consent)
        model.retryMove(consent)
        for _ in 0..<300 where model.moveNotices.current == nil { try await Task.sleep(for: .milliseconds(10)) }
        let moved = try XCTUnwrap(model.moveNotices.current)
        XCTAssertEqual(ParityProtocol.state.moves.count, 2)
        XCTAssertEqual(ParityProtocol.state.moves.last?["stopTerminal"] as? Bool, true)
        XCTAssertEqual(moved.title, "Moved to peer")
        XCTAssertTrue(moved.message.contains("Stopped the terminal process 41 that held it."))
        XCTAssertTrue(moved.message.contains("Add their credentials again on peer:\nlinear"))
        XCTAssertNil(moved.retry)
    }

    @MainActor
    func testLifecycleActionsGoThroughTheComputerThatListsTheSessionWithRetryKeys() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let capabilities = #""capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}"#
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data("""
        {"updatedAt":"now","daemon":{"running":true,"hosts":[{"id":"hpeer","name":"peer","state":"connected"}]},"surfaces":[],"sessions":[
          {"id":"old","surface":"codex","name":"task","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities)},
          {"id":"m1","surface":"codex","name":"remote","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities),"host":{"id":"hpeer","name":"peer","state":"connected"}}],
         "totalSessions":2,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}
        """.utf8)))

        await model.resume("computer::old")
        XCTAssertNil(model.operationError)
        model.stopAndResume("hpeer::m1")
        for _ in 0..<200 where ParityProtocol.state.actions.count < 3 { try await Task.sleep(for: .milliseconds(10)) }

        let sent = ParityProtocol.state.actions.map { "\($0["action"] as? String ?? "") \($0["sessionId"] as? String ?? "")" }
        XCTAssertEqual(sent, ["session-lifecycle-resume old", "session-lifecycle-stop m1", "session-lifecycle-resume m1"])
        XCTAssertTrue(ParityProtocol.state.actionIdempotencyKeys.allSatisfy { $0 != nil }, "every lifecycle action carries a retry key")
        XCTAssertNil(model.operationError)
    }

    @MainActor
    func testSessionOperationsReachTheComputerThatListsTheSession() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let capabilities = #""capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true,"lifecycle":{"status":true,"logs":true,"stop":true,"resume":true}}"#
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data("""
        {"updatedAt":"now","daemon":{"running":true,"hosts":[{"id":"hpeer","name":"peer","state":"connected"}]},"surfaces":[],"sessions":[
          {"id":"hpeer::m1","surface":"codex","name":"remote","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities),"host":{"id":"hpeer","name":"peer","state":"connected"}}],
         "totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}
        """.utf8)))
        let session = try XCTUnwrap(model.session("hpeer::m1"))
        XCTAssertTrue(SessionOperationAvailability.nativeQueueWritable(session))
        XCTAssertEqual(SessionOperationAvailability.lifecycle(session), SessionLifecycle(status: true, logs: true, stop: true, resume: true))

        let target = model.operationTarget(session.id)
        XCTAssertNil(target.unavailable)
        XCTAssertEqual(target.sessionID, "hpeer::m1")
        let api = try XCTUnwrap(target.api)

        let queue = NativeQueueController(keys: target.keys)
        queue.reset(sessionID: target.sessionID)
        await queue.reload(api)
        XCTAssertEqual(queue.items.map(\.id), ["q1", "q2"])
        let first = try XCTUnwrap(queue.items.first)
        let added = await queue.add("three", api: api)
        let updated = await queue.update(first, text: "uno", api: api)
        await queue.move(first, by: 1, api: api)
        let startedNext = await queue.start(nil, api: api)
        let startedNow = await queue.start(first, api: api)
        await queue.delete(first, api: api)
        XCTAssertTrue(added && updated && startedNext && startedNow)
        XCTAssertNil(queue.error)

        let lifecycle = BackgroundSessionController(keys: target.keys)
        lifecycle.reset(sessionID: target.sessionID)
        for action in ["status", "logs", "stop", "resume"] {
            let ran = await lifecycle.run(action, api: api)
            XCTAssertTrue(ran, action)
        }
        XCTAssertNil(lifecycle.error)

        try await model.fork(session)

        let sent = ParityProtocol.state.actions.map { body in
            let op = (body["nativeQueue"] as? [String: Any]).map { " " + ($0["queueAction"] as? String ?? "") + ($0["queuedSubmissionId"].map { " \($0)" } ?? "") } ?? ""
            return "\(body["action"] as? String ?? "") \(body["sessionId"] as? String ?? "")" + op
        }
        XCTAssertEqual(sent, [
            "native-queue hpeer::m1 list",
            "native-queue hpeer::m1 add", "native-queue hpeer::m1 list",
            "native-queue hpeer::m1 update q1", "native-queue hpeer::m1 list",
            "native-queue hpeer::m1 reorder", "native-queue hpeer::m1 list",
            "native-queue hpeer::m1 start", "native-queue hpeer::m1 list",
            "native-queue hpeer::m1 start q1", "native-queue hpeer::m1 list",
            "native-queue hpeer::m1 delete q1", "native-queue hpeer::m1 list",
            "session-lifecycle-status hpeer::m1", "session-lifecycle-logs hpeer::m1",
            "session-lifecycle-stop hpeer::m1", "session-lifecycle-status hpeer::m1",
            "session-lifecycle-resume hpeer::m1", "session-lifecycle-status hpeer::m1",
            "session-fork hpeer::m1",
        ])
        let reorder = ParityProtocol.state.actions[5]["nativeQueue"] as? [String: Any]
        XCTAssertEqual(reorder?["queuedSubmissionIds"] as? [String], ["q2", "q1"])
        XCTAssertNil(ParityProtocol.state.actions.last?["fork"], "a whole-session fork names no turn")
        for (body, key) in zip(ParityProtocol.state.actions, ParityProtocol.state.actionIdempotencyKeys) {
            let list = (body["nativeQueue"] as? [String: Any])?["queueAction"] as? String == "list"
            let reads = ["session-lifecycle-status", "session-lifecycle-logs"].contains(body["action"] as? String ?? "")
            if !list && !reads { XCTAssertNotNil(key, "\(body["action"] ?? "") carries a retry key") }
        }
        XCTAssertEqual(model.requestedSessionID, "hpeer::forked")

        let missing = model.operationTarget("gone::s9")
        XCTAssertNil(missing.api)
        XCTAssertEqual(missing.unavailable, "The computer running this session can't be reached.")
    }

    @MainActor
    func testProjectsAreManagedThroughTheComputerThatListsThem() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let capabilities = #""capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}"#
        let project = #"{"id":"p1","displayName":"one","rootPath":"/p1","kind":"git","added":true}"#
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data("""
        {"updatedAt":"now","daemon":{"running":true,"hosts":[{"id":"hpeer","name":"peer","state":"connected"}]},"surfaces":[],"sessions":[
          {"id":"s-lead","surface":"claude","name":"lead","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities),"cwd":"/p1","project":\(project)},
          {"id":"s2","surface":"claude","name":"helper","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities),"cwd":"/p1","project":\(project)},
          {"id":"m1","surface":"codex","name":"remote","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities),"cwd":"/srv/tool","host":{"id":"hpeer","name":"peer","state":"connected"}},
          {"id":"m2","surface":"claude","name":"remote lead","status":"idle","queueCount":0,"open":true,"current":false,\(capabilities),"cwd":"/srv/repo","host":{"id":"hpeer","name":"peer","state":"connected"},"project":{"id":"repo:shared","displayName":"shared","rootPath":"/srv/repo","kind":"git"}}],
         "totalSessions":4,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,
         "projects":[
          {"id":"p1","displayName":"one","checkouts":[{"host":{"id":"computer","name":"computer","state":"local"},"projectId":"p1","rootPath":"/p1","added":true},{"host":{"id":"hpeer","name":"peer","state":"connected"},"projectId":"hpeer::p1","rootPath":"/srv/p1","added":true}],"coordinators":[{"host":{"id":"computer","name":"computer","state":"local"},"name":"lead","surface":"claude","sessionId":"s-lead"}]},
          {"id":"repo:shared","displayName":"shared","checkouts":[{"host":{"id":"hpeer","name":"peer","state":"connected"},"projectId":"repo:shared","rootPath":"/srv/repo"}],"coordinators":[{"host":{"id":"hpeer","name":"peer","state":"connected"},"name":"remote","surface":"claude","sessionId":"m2"}]},
                    {"id":"p-empty","displayName":"empty","checkouts":[{"host":{"id":"computer","name":"computer","state":"local"},"projectId":"p-empty","rootPath":"/empty","added":true}],"coordinators":[]}]}
        """.utf8)))
        XCTAssertEqual(Set(model.projects.keys), ["computer::p1", "repo:shared", "computer::p-empty"])
        let sessions = try XCTUnwrap(model.snapshot?.sessions)
        let lead = try XCTUnwrap(sessions.first { $0.id == "computer::s-lead" })
        let helper = try XCTUnwrap(sessions.first { $0.id == "computer::s2" })
        let remote = try XCTUnwrap(sessions.first { $0.id == "hpeer::m1" })
        XCTAssertFalse(model.canMakeLead(lead))
        XCTAssertTrue(model.canMakeLead(helper))
        XCTAssertTrue(model.canAddFolder(remote))
        let mirroredLead = try XCTUnwrap(sessions.first { $0.id == "hpeer::m2" })
        XCTAssertFalse(model.canMakeLead(mirroredLead), "a lead reached through a mirror is not offered to lead again")
        XCTAssertEqual(model.projects[try XCTUnwrap(mirroredLead.project?.id)]?.state.id, "repo:shared")

        let asked = await model.makeLead(helper.id, replace: false)
        XCTAssertEqual(asked, .confirmReplace("/p1 already has coordinator @lead; replace it to take over. Replace it?"))
        let replaced = await model.makeLead(helper.id, replace: true)
        XCTAssertEqual(replaced, .done("@helper leads /p1."))
        XCTAssertEqual(ParityProtocol.state.actions.last?["sessionId"] as? String, "s2")

        _ = await model.addFolder(of: remote)
        let added = try XCTUnwrap(ParityProtocol.state.actions.last)
        XCTAssertEqual(added["action"] as? String, "project-add")
        XCTAssertEqual(added["path"] as? String, "/srv/tool")
        XCTAssertEqual(added["host"] as? String, "hpeer")

        let listed = try XCTUnwrap(model.projects["computer::p1"])
        _ = await model.removeProject("hpeer::p1", of: listed)
        XCTAssertEqual(ParityProtocol.state.actions.last?["action"] as? String, "project-remove")
        XCTAssertEqual(ParityProtocol.state.actions.last?["projectId"] as? String, "hpeer::p1")
        XCTAssertEqual(model.creationHost(for: listed.state.checkouts[1], of: listed), "hpeer")
        XCTAssertEqual(model.creationHost(for: listed.state.checkouts[0], of: listed), "computer")
    }

    @MainActor
    func testArchivingARepositoryProjectUsesItsSharedID() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[{"id":"s1","surface":"codex","name":"task","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"project":{"id":"repo:github.com/acme/widget","displayName":"widget","rootPath":"/w","kind":"git"}}],"totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#.utf8)))
        XCTAssertEqual(model.snapshot?.sessions.first?.project?.id, "repo:github.com/acme/widget")
        await model.setProjectArchived("repo:github.com/acme/widget", true)
        XCTAssertNil(model.operationError)
        let archived = try XCTUnwrap(ParityProtocol.state.actions.last)
        XCTAssertEqual(archived["action"] as? String, "project-archive")
        XCTAssertEqual(archived["projectId"] as? String, "repo:github.com/acme/widget")
    }

    private static let codexSessionSnapshot = #"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[{"id":"old","surface":"codex","name":"task","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}],"totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#

    @MainActor
    func testCreatingOnAMirroredComputerKeysTheSessionByItsOwner() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"now","daemon":{"running":true,"hosts":[{"id":"hpeer","name":"peer","state":"connected"}]},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}"#.utf8)))
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen", host: "hpeer")
        XCTAssertTrue(created)
        XCTAssertEqual(ParityProtocol.state.actions.first?["host"] as? String, "hpeer")
        XCTAssertEqual(model.requestedSessionID, "hpeer::created")
    }

    @MainActor private func makeModel() -> AgenthailIOSModel {
        AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string:"https://fixture.invalid")!, token:"fixture", session: FixtureScope.open(self).session([ParityProtocol.self])))
    }
}

private final class ParityProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var records: [[String:Any]] = []
        private var unknown = false
        private var fail = false
        private var accepted = false
        private var failReceipt = false
        private var create: (Int, String)?
        private var actionKeys: [String?] = []
        private var queries: [String] = []
        private var moveBodies: [[String:Any]] = []
        private var moveReplies: [(Int, String)] = []
        var moves: [[String:Any]] { lock.withLock { moveBodies } }
        func answerMoves(_ replies: [(Int, String)]) { lock.withLock { moveBodies = []; moveReplies = replies } }
        func move(_ body: [String:Any]) -> (Int, String) { lock.withLock {
            moveBodies.append(body)
            return moveReplies.isEmpty ? (500, #"{"error":{"message":"no move reply"}}"#) : moveReplies.removeFirst()
        } }
        var promptQueries: [String] { lock.withLock { queries } }
        func recordQuery(_ query: String) { lock.withLock { queries.append(query) } }
        var actions: [[String:Any]] { lock.withLock { records } }
        var actionIdempotencyKeys: [String?] { lock.withLock { actionKeys } }
        func reset(unknown: Bool = false, fail: Bool = false, accepted: Bool = false, failReceipt: Bool = false, create: (Int, String)? = nil) { lock.withLock { records = []; actionKeys = []; queries = []; self.unknown = unknown; self.fail = fail; self.accepted = accepted; self.failReceipt = failReceipt; self.create = create } }
        func respond(_ body: [String:Any], idempotencyKey: String?) -> (Int,String) { lock.withLock {
            records.append(body)
            actionKeys.append(idempotencyKey)
            if fail { return (502,#"{"error":{"message":"unavailable"}}"#) }
            if failReceipt { return (409,#"{"error":{"code":"create_failed","message":"launcher busy"}}"#) }
            if body["action"] as? String == "coordinator-set" {
                if body["replace"] as? Bool != true { return (409, #"{"error":{"code":"coordinator_exists","message":"/p1 already has coordinator @lead; replace it to take over"}}"#) }
                return (200, #"{"ok":true,"coordinator":{"name":"helper","projectRoot":"/p1"}}"#)
            }
            if let action = body["action"] as? String, action.hasPrefix("session-lifecycle-") {
                return (200, #"{"ok":true,"result":{"sessionId":"\#(body["sessionId"] as? String ?? "")","state":"running","action":"\#(action)"}}"#)
            }
            if body["action"] as? String == "session-fork" {
                return (200, #"{"ok":true,"result":{"session":{"id":"forked"}}}"#)
            }
            if body["action"] as? String == "native-queue" {
                if (body["nativeQueue"] as? [String: Any])?["queueAction"] as? String == "list" {
                    return (200, #"{"ok":true,"result":{"data":[{"id":"q1","clientUserMessageId":"c1","input":[{"type":"text","text":"one"}]},{"id":"q2","clientUserMessageId":"c2","input":[{"type":"text","text":"two"}]}],"nextCursor":null}}"#)
                }
                return (200, #"{"ok":true,"result":{}}"#)
            }
            if (body["action"] as? String)?.contains("create") == true {
                if let create { return create }
                if accepted { return (202, #"{"ok":true,"status":"submitted","accepted":true,"retryable":false,"launcher":"tmux","warning":"Submitted to tmux."}"#) }
                if body["surface"] as? String == "claude" {
                    return unknown ? (502, #"{"error":{"code":"create_unknown","message":"registration delayed"}}"#) : (201, #"{"ok":true,"session":{"id":"native-claude","surface":"claude","name":"phone-task","status":"unknown","lastActive":"2026-09-08T08:00:00Z"},"result":null}"#)
                }
                return (unknown ? 202 : 201, unknown ? #"{"ok":true,"status":"submitted","accepted":true,"sessionId":"created"}"# : #"{"ok":true,"sessionId":"created"}"#)
            }
            return (200,#"{"ok":true}"#)
        } }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() { served(answer) }

    private func answer() {
        var status = 200
        var text = #"{"surfaces":[{"id":"codex","workspace":true}],"projects":[{"id":"p-project","displayName":"project","rootPath":"/project","kind":"git"}]}"#
        if request.url?.path == "/api/v1/actions" {
            var data = request.httpBody ?? Data()
            if let stream = request.httpBodyStream { stream.open(); defer { stream.close() }; var buffer = [UInt8](repeating:0,count:4096); while stream.hasBytesAvailable { let n = stream.read(&buffer,maxLength:buffer.count); if n <= 0 { break }; data.append(contentsOf:buffer.prefix(n)) } }
            let body = (try? JSONSerialization.jsonObject(with:data)) as? [String:Any] ?? [:]
            (status,text) = Self.state.respond(body, idempotencyKey: request.value(forHTTPHeaderField: "Idempotency-Key"))
        } else if request.url?.path == "/api/v1/session-moves" {
            var data = request.httpBody ?? Data()
            if let stream = request.httpBodyStream { stream.open(); defer { stream.close() }; var buffer = [UInt8](repeating:0,count:4096); while stream.hasBytesAvailable { let n = stream.read(&buffer,maxLength:buffer.count); if n <= 0 { break }; data.append(contentsOf:buffer.prefix(n)) } }
            (status,text) = Self.state.move((try? JSONSerialization.jsonObject(with:data)) as? [String:Any] ?? [:])
        } else if request.url?.path == "/api/v1/prompts" {
            Self.state.recordQuery(request.url?.query ?? "")
            text = #"{"prompts":[{"sessionId":"old","itemId":"i2","turnId":"t2","ts":"2026-10-20T10:00:02Z","text":"second"},{"sessionId":"old","itemId":"i1","turnId":"t1","ts":"2026-10-20T10:00:01Z","text":"first"}],"nextBefore":""}"#
        } else if request.url?.path != "/api/v1/session-options" { status = 503; text = #"{"error":{"message":"fixture refresh unavailable"}}"# }
        client?.urlProtocol(self,didReceive:HTTPURLResponse(url:request.url!,statusCode:status,httpVersion:nil,headerFields:["Content-Type":"application/json"])!,cacheStoragePolicy:.notAllowed)
        client?.urlProtocol(self,didLoad:Data(text.utf8)); client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
