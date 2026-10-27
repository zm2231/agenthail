export const critical = (meter) => meter.severity === "critical" || meter.percent >= 90;

const worse = (a, b) => Number(critical(a)) - Number(critical(b)) || a.percent - b.percent;

function chip(group, members) {
  const rated = members.filter((account) => account.meters.length);
  const top = rated.map((account) => ({ account, meter: account.meters.reduce((best, meter) => (worse(meter, best) > 0 ? meter : best)) }))
    .reduce((best, entry) => (!best || worse(entry.meter, best.meter) > 0 ? entry : best), null);
  const lead = top?.account || members[0];
  const suffix = ` · ${group.name}`;
  const name = lead.title.endsWith(suffix) ? lead.title.slice(0, -suffix.length) : lead.title;
  return {
    ...lead,
    id: `proxy:${group.id}`,
    title: top ? `${group.name} · ${name}` : lead.provider === "proxy" ? lead.title : group.name,
    meters: top ? [top.meter] : [],
    worst: top?.meter || null,
    critical: top ? critical(top.meter) : false,
    problem: top ? null : lead.problem,
    spoken: members.map((account) => account.spoken).join(". "),
  };
}

export function strip(list) {
  const groups = new Map();
  for (const account of list) {
    if (account.group) groups.set(account.group.id, [...(groups.get(account.group.id) || []), account]);
  }
  const placed = new Set();
  return list.flatMap((account) => {
    if (!account.group) return [account];
    if (placed.has(account.group.id)) return [];
    placed.add(account.group.id);
    return [chip(account.group, groups.get(account.group.id))];
  });
}
