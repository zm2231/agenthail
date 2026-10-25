export function livePlan(session) {
  const plan = session.plan;
  if (!session.open || session.status === "offline" || session.unavailableReason || !plan?.total || plan.done >= plan.total) return null;
  return plan;
}

export function planDetail(plan) {
  const done = `${plan.done} of ${plan.total} tasks done`;
  return plan.current ? `${done} · ${plan.current}` : done;
}

