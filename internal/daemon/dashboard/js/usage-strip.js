import { wait } from "./usage-proxy.js";

export const critical = (meter) => meter.severity === "critical" || meter.percent >= 90;

const worse = (a, b) => Number(critical(a)) - Number(critical(b)) || a.percent - b.percent;

const fullest = (account) => account.meters.reduce((best, meter) => (!best || worse(meter, best) > 0 ? meter : best), null);

// The daemon names the account the proxy routes to; an older one does not, and then the fullest stands in.
function chip(group, members, now) {
  const routed = group.routing?.account ? members.find((account) => account.proxyAccount?.id === group.routing.account) : null;
  const top = routed
    ? { account: routed, meter: fullest(routed) }
    : members.filter((account) => account.meters.length).map((account) => ({ account, meter: fullest(account) }))
      .reduce((best, entry) => (!best || worse(entry.meter, best.meter) > 0 ? entry : best), null);
  const lead = top?.account || members[0];
  const meter = top?.meter || null;
  const suffix = ` · ${group.name}`;
  const name = lead.title.endsWith(suffix) ? lead.title.slice(0, -suffix.length) : lead.title;
  let title = lead.provider === "proxy" ? lead.title : `${group.name} · ${name}`;
  if (routed && group.routing.full) {
    const frees = Date.parse(group.routing.freesAt || "");
    title = `${group.name} · full${frees > now ? `, frees in ${wait(frees - now)}` : ""}`;
  }
  return {
    ...lead,
    id: `proxy:${group.id}`,
    title,
    meters: meter ? [meter] : [],
    worst: meter,
    critical: meter ? critical(meter) : false,
    problem: meter ? null : lead.problem,
    spoken: members.map((account) => account.spoken).join(". "),
  };
}

export function strip(list, now = Date.now()) {
  const groups = new Map();
  for (const account of list) {
    if (account.group) groups.set(account.group.id, [...(groups.get(account.group.id) || []), account]);
  }
  const placed = new Set();
  return list.flatMap((account) => {
    if (!account.group) return [account];
    if (placed.has(account.group.id)) return [];
    placed.add(account.group.id);
    return [chip(account.group, groups.get(account.group.id), now)];
  });
}
