import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const { settings } = await dashboardModules(["settings"], {});
const plain = (value) => JSON.parse(JSON.stringify(value));
const saved = { codexRecentHours: 12, busyDelivery: "steer" };

assert.equal(settings.dashboardConfigChange(saved, 12, "steer"), null, "an unchanged form sends nothing");
assert.deepEqual(plain(settings.dashboardConfigChange(saved, 12, "queue")), { action: "dashboard-config", busyDelivery: "queue" }, "changing delivery leaves the window out");
assert.deepEqual(plain(settings.dashboardConfigChange(saved, 24, "steer")), { action: "dashboard-config", codexRecentHours: 24 }, "changing the window leaves delivery out");
assert.deepEqual(plain(settings.dashboardConfigChange(saved, 3, "queue")), { action: "dashboard-config", codexRecentHours: 3, busyDelivery: "queue" }, "changing both sends both");
assert.equal(settings.dashboardConfigChange({ codexRecentHours: 5 }, 5, "queue"), null, "a saved config with no delivery reads as queue");

console.log("dashboard settings: ok");
