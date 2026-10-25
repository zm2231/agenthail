import { app } from "./state.js";
import { signinRequest } from "./http.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { toast } from "./feedback.js";

export function signinOffHint(detail) {
  const claude = /^Claude Remote Control/.test(detail);
  const consumer = claude ? "Claude sessions reached over Remote Control read" : "Notion reads";
  return `Browser sign-in is off, so ${consumer} no cookies. Turn it on in Settings > Browser Sign-in, or run agenthail signin use ${claude ? "claude" : "notion"} auto.`;
}

export function signinResolved(consumer) {
  if (consumer.mode === "off") return "Off. Reads no cookies.";
  if (!consumer.browserLabel) return "Uses the system default browser.";
  const name = consumer.profileName && consumer.profileName !== consumer.profile ? ` (${consumer.profileName})` : "";
  const where = consumer.browser === "safari" ? "Safari" : `${consumer.browserLabel} · ${consumer.profile}${name}`;
  if (consumer.mode === "automatic") return `Automatic: ${where}, the system default browser.`;
  return `${consumer.profileName}: ${consumer.browserLabel} · ${consumer.profile}.`;
}

export async function loadSignin() {
  try {
    app.signin = await signinRequest("/api/v1/signin");
    renderSignin();
  } catch (error) {
    $("#signin-consumers").innerHTML = `<div class="empty-card">${escape(error.message)}</div>`;
  }
}

export function signinProfileLabel(profile) {
  const browser = (app.signin.browsers || []).find((item) => item.browser === profile.browser);
  return `${browser?.label || profile.browser} · ${profile.profile}`;
}

export function renderSignin() {
  const signin = app.signin;
  if (!signin) return;
  const profiles = signin.profiles || [];
  $("#signin-consumers").innerHTML = (signin.consumers || []).map((consumer) => {
    const options = [
      `<option value="auto"${consumer.mode === "automatic" ? " selected" : ""}>Automatic (system default browser)</option>`,
      ...profiles.map((profile) => `<option value="${escape(profile.id)}"${consumer.profileId === profile.id ? " selected" : ""}>${escape(profile.label)}</option>`),
      `<option value="none"${consumer.mode === "off" ? " selected" : ""}>Off</option>`,
    ].join("");
    const state = consumer.mode === "off" ? '<span class="health-state signin_off">Off</span>' : consumer.unavailable ? '<span class="health-state signin_off">No browser here</span>' : consumer.problem ? '<span class="health-state unavailable">Cannot read</span>' : "";
    const problem = consumer.unavailable ? `<p class="signin-resolved">${escape(consumer.unavailable)}</p>` : consumer.problem ? `<p class="signin-problem">${escape(consumer.problem)}</p>` : "";
    return `<div class="signin-consumer"><div><strong>${escape(consumer.label)}</strong>${state}<p>${escape(consumer.purpose)}</p><p class="signin-resolved">${escape(signinResolved(consumer))}</p>${problem}</div><label><span class="sr-only">Sign-in profile for ${escape(consumer.label)}</span><select data-signin-consumer="${escape(consumer.id)}">${options}</select></label></div>`;
  }).join("");
  $("#signin-profiles").innerHTML = profiles.map((profile) =>
    `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(profile.label)}</div><div class="operation-detail">${escape(signinProfileLabel(profile))}</div></div><button class="button quiet" data-signin-remove="${escape(profile.id)}" type="button">Remove</button></article>`,
  ).join("") || '<div class="empty-card">No pinned profiles. Add one below to use a profile other than the default browser\'s.</div>';
  const available = (signin.browsers || []).filter((browser) => browser.available && browser.profiles.some((profile) => !profile.profileId));
  const browserSelect = $("#signin-browser");
  const previous = browserSelect.value;
  browserSelect.innerHTML = available.map((browser) => `<option value="${escape(browser.browser)}">${escape(browser.label)}</option>`).join("") || '<option value="">No browser profiles to add</option>';
  if (available.some((browser) => browser.browser === previous)) browserSelect.value = previous;
  renderSigninProfileChoices();
  const unavailable = (signin.browsers || []).filter((browser) => !browser.available);
  const note = $("#signin-unavailable");
  note.hidden = unavailable.length === 0;
  note.textContent = unavailable.map((browser) => `${browser.label} is unavailable: ${browser.detail}.`).join(" ");
}

export function renderSigninProfileChoices() {
  const browser = (app.signin?.browsers || []).find((item) => item.browser === $("#signin-browser").value);
  const choices = (browser?.profiles || []).filter((profile) => !profile.profileId);
  $("#signin-profile").innerHTML = choices.map((profile) => `<option value="${escape(profile.profile)}">${escape(profile.name)}${profile.name === profile.profile ? "" : ` (${escape(profile.profile)})`}</option>`).join("");
  $("#signin-add-form button[type=submit]").disabled = choices.length === 0;
}

export function bindSigninEvents() {
  $("#signin-browser").addEventListener("change", renderSigninProfileChoices);
  $("#signin-add-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const { profile } = await signinRequest("/api/v1/signin/profiles", { method: "POST", body: JSON.stringify({ browser: $("#signin-browser").value, profile: $("#signin-profile").value, label: $("#signin-label").value }) });
      $("#signin-label").value = "";
      await loadSignin();
      toast(`Added ${profile.label}. Choose it for Notion or Claude Remote Control above.`);
    } catch (error) {
      toast(error.message);
    }
  });
  $("#signin-card").addEventListener("change", async (event) => {
    const select = event.target.closest("[data-signin-consumer]");
    if (!select) return;
    select.disabled = true;
    try {
      const consumer = await signinRequest("/api/v1/signin/bindings", { method: "PUT", body: JSON.stringify({ consumer: select.dataset.signinConsumer, profileId: select.value }) });
      await loadSignin();
      toast(consumer.mode === "off" ? `${consumer.label} sign-in is off. No cookies will be read.` : `${consumer.label}: ${signinResolved(consumer)}`);
    } catch (error) {
      toast(error.message);
      await loadSignin();
    }
  });
  $("#signin-card").addEventListener("click", async (event) => {
    const remove = event.target.closest("[data-signin-remove]");
    if (!remove) return;
    remove.disabled = true;
    try {
      const { profile } = await signinRequest(`/api/v1/signin/profiles?id=${encodeURIComponent(remove.dataset.signinRemove)}`, { method: "DELETE" });
      await loadSignin();
      toast(`Removed ${profile.label}.`);
    } catch (error) {
      toast(error.message);
      remove.disabled = false;
    }
  });
}
