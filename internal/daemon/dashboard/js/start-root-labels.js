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

export function rootUsage(root) {
  const parts = [];
  for (const [label, test] of [["session", (kind) => kind === "session" || kind === "5h"], ["week", (kind) => kind === "weekly" || kind === "weekly_all"]]) {
    const limit = (root.usage?.limits || []).find((item) => test(item.kind));
    if (limit) parts.push(`${label} ${Math.round(limit.percent)}%`);
  }
  return parts.join(", ");
}

export function shortReason(reason) {
  return reason.split(/[,;]/)[0];
}

export function pickedTitle(pick, roots, titles) {
  const index = roots.findIndex((root) => (pick.root ? root.root === pick.root : root.primary));
  return index >= 0 ? titles[index] : pick.name;
}
