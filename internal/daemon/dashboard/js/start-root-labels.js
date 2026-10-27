const kinds = { api_key: "API key", signed_out: "signed out" };

export function rootTitles(roots, host) {
  const names = roots.map((root) => {
    if (root.proxy) return `${root.proxy} via proxy`;
    const email = root.signedInAs?.email;
    if (email) return email.split("@")[0];
    return `${host || "this host"} (${kinds[root.login] || "signed out"})`;
  });
  return names.map((name, index) => (names.filter((other) => other === name).length > 1 ? `${name} · ${roots[index].name}` : name));
}

export function rootUsage(root, policy) {
  const parts = [];
  let over = false;
  for (const [label, test, ceiling] of [["session", (kind) => kind === "session" || kind === "5h", policy?.sessionCeiling], ["week", (kind) => kind === "weekly" || kind === "weekly_all", policy?.weeklyCeiling]]) {
    const limit = (root.usage?.limits || []).find((item) => test(item.kind));
    if (!limit) continue;
    parts.push(`${label} ${Math.round(limit.percent)}%`);
    if (ceiling && limit.percent >= ceiling) over = true;
  }
  if (over) parts.push("over limit");
  return parts.join(", ");
}

export function shortReason(reason) {
  return reason.split(/[,;]/)[0];
}

export function pickedTitle(pick, roots, titles) {
  const index = roots.findIndex((root) => (pick.root ? root.root === pick.root : root.primary));
  return index >= 0 ? titles[index] : pick.name;
}
