export let tools;

export let toolsLoading = null;

export function loadTools() {
  toolsLoading ||= import("./tool-activity.js").then(
    (module) => { tools = module; },
    (error) => { toolsLoading = null; throw error; },
  );
  return toolsLoading;
}
