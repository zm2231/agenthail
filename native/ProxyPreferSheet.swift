import SwiftUI

struct ProxyPreferRequest: Identifiable {
    let account: UsageAccount
    let set: ProxyPreferSet
    var id: String { account.id + "|" + set.rawValue }
}

struct ProxyPreferSheet: View {
    let api: AgenthailAPI?
    let request: ProxyPreferRequest
    let applied: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var plan: ProxyPreferPlan?
    @State private var problem: String?
    @State private var moveNow = false
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(request.account.login + " on the " + (request.account.group?.name ?? "") + " proxy").font(.headline)
            if let plan {
                ForEach(Array(plan.changeLines.enumerated()), id: \.offset) { line in
                    Text(line.element.text).fixedSize(horizontal: false, vertical: true)
                        .help(line.element.help ?? "")
                }
                if !plan.affectLines.isEmpty {
                    Text("Affects").font(.subheadline.weight(.semibold)).foregroundStyle(.secondary).padding(.top, 4)
                    ForEach(Array(plan.affectLines.enumerated()), id: \.offset) { line in
                        Text(line.element).fixedSize(horizontal: false, vertical: true)
                    }
                }
                ForEach(Array(plan.notes.enumerated()), id: \.offset) { line in
                    Text(line.element).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            } else if problem == nil {
                ProgressView("Reading the proxy…")
            }
            if request.set == .only {
                Toggle("Also move the running sessions on the accounts it turns off", isOn: $moveNow)
            }
            if let problem {
                Text(problem).foregroundStyle(.red).fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("proxy-prefer-problem")
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                Button { Task { await apply() } } label: {
                    Text(plan?.confirm ?? "Confirm").multilineTextAlignment(.center).lineLimit(3)
                        .frame(maxWidth: 280).fixedSize(horizontal: false, vertical: true)
                }
                    .buttonStyle(.borderedProminent)
                    .disabled(plan == nil || plan?.nothing != nil || working)
                    .accessibilityIdentifier("proxy-prefer-confirm")
            }
        }
        .padding(20)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("proxy-prefer-sheet")
        .task(id: moveNow) { await load() }
    }

    private func load() async {
        guard let api, let reference = request.account.proxyAccount else {
            problem = "No connected computer can read the " + (request.account.group?.name ?? "") + " proxy."
            return
        }
        plan = nil
        do {
            plan = try await api.proxyPreferPlan(reference, set: request.set, now: moveNow)
            problem = nil
        } catch {
            if error is CancellationError || (error as? URLError)?.code == .cancelled { return }
            problem = AgenthailAPIError.userMessage(error)
        }
    }

    private func apply() async {
        guard let api, let plan, let reference = request.account.proxyAccount else { return }
        working = true
        defer { working = false }
        do {
            let result = try await api.proxyPrefer(reference, set: request.set, now: moveNow, planId: plan.planId)
            applied(result.message ?? "")
            dismiss()
        } catch {
            let message = AgenthailAPIError.userMessage(error)
            await load()
            problem = message
        }
    }
}
