const time = (value) => {
  const parsed = Date.parse(value || "");
  return Number.isNaN(parsed) ? null : parsed;
};

function wait(ms) {
  const minutes = Math.max(Math.round(ms / 60_000), 1);
  const hours = Math.floor(minutes / 60);
  if (hours >= 24) return `${Math.floor(hours / 24)}d ${hours % 24}h`;
  return hours > 0 ? `${hours}h ${minutes % 60}m` : `${minutes}m`;
}

export function accountState(account, now) {
  if (account.status === "cooling") {
    const retry = time(account.nextRetry);
    return retry !== null && retry > now ? `cooling for ${wait(retry - now)}` : "cooling";
  }
  if (account.status === "disabled" || account.status === "unavailable") return account.status;
  return null;
}

export function proxyGroup(set, sets) {
  const id = set.proxy?.id || `${set.hostId}|${set.name}`;
  const roots = sets.filter((other) => other.provider === "claude" && other.proxy?.id && other.proxy.id === set.proxy?.id).map((other) => ({ hostId: other.hostId, host: other.host, root: other.root, name: other.name }));
  return { id, name: set.name, title: `${set.name} (proxy)`, url: set.proxy?.url || null, host: set.host, hostId: set.hostId, local: set.local === true, problem: set.problem || null, problemDetail: set.problemDetail || null, roots };
}

export function expandProxies(sets, now) {
  const expanded = [];
  for (const set of sets || []) {
    if (set.provider === "claude" && set.proxy) continue;
    if (set.provider !== "proxy") {
      expanded.push(set);
      continue;
    }
    const group = proxyGroup(set, sets);
    if (!(set.accounts || []).length) {
      expanded.push({ host: set.host, hostId: set.hostId, local: set.local, provider: "proxy", name: set.name, meters: [], problem: set.problem, problemDetail: set.problemDetail, group });
      continue;
    }
    for (const account of set.accounts) {
      expanded.push({ host: set.host, hostId: set.hostId, local: set.local, provider: account.provider, account: account.label, name: set.name, root: `proxy:${group.id}:${account.id}`, meters: account.meters || [], asOf: account.asOf, problem: accountState(account, now), group });
    }
  }
  return expanded;
}

export function ownsByProxy(group, session) {
  if (session.archived || session.surface !== "claude") return false;
  return group.roots.some((root) => root.hostId === session.host?.id && root.root === session.claudeConfigRoot?.root);
}
