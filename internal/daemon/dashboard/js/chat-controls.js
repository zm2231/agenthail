import { action } from "./actions.js";
import { toast } from "./feedback.js";
import { load } from "./dashboard-state.js";
import { app } from "./state.js";
import { selectSession } from "./session-selection.js";

export function bindChatEvents() {
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest("[data-tool]");
    if (!form) return;
    event.preventDefault();
    const tool = form.dataset.tool;
    const input = form.querySelector("input");
    try {
      if (tool === "alias") {
        await action("alias", { alias: input.value.trim() });
        toast("Conversation name saved.");
        await load();
      } else if (tool === "goal-budget") {
        const value = input.value.trim();
        await action("goal-budget", { message: value });
        toast(value ? "Token budget saved." : "Token budget cleared.");
      } else {
        const result = await action(app.history?.goal?.objective ? "goal-edit" : "goal-set", { message: input.value.trim() });
        toast(result?.result?.evidence === "queued" ? result.result.detail : "Goal saved.");
      }
      await selectSession(app.selected.id);
    } catch (error) {
      toast(error.message);
    }
  });
}
