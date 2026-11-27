fn main() -> anyhow::Result<()> {
    let client = feed::catalog::Client::from_env()?;
    let t = std::time::Instant::now();
    let mut overview = feed::derive::live::Overview::default();
    overview.refresh(feed::derive::live::fetch(&client, &Default::default(), None)?);
    let fetched = t.elapsed();
    let due = overview.due_tails(&Default::default());
    let results: Vec<_> = std::thread::scope(|scope| {
        let jobs: Vec<_> = due.into_iter().map(|(id, at, held)| scope.spawn(|| (feed::derive::tails::fetch(&client, &id, feed::derive::tails::READ_BUDGET, held), id, at))).collect();
        jobs.into_iter().filter_map(|h| h.join().ok()).collect()
    });
    let tails = results.iter().filter(|((r, _), _, _)| r.as_ref().is_ok_and(|tail| !tail.items.is_empty())).count();
    for ((result, held), id, at) in results {
        overview.take_tail(&id, at, result, held);
    }
    let w = overview.world(&[]);
    let needs = w.needs();
    println!("{fetched:?} overview, {:?} with tails: {} sessions, {} projects, {} work items, {} needs, {} hosts, {} meters, {tails} tails, {} hands-on", t.elapsed(), w.sessions.len(), w.projects.len(), w.work.len(), needs.len(), w.hosts.len(), w.usage.len(), w.hands_on(&[]).len());
    Ok(())
}
