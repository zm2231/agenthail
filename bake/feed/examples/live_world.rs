fn main() -> anyhow::Result<()> {
    let client = feed::catalog::Client::from_env()?;
    let t = std::time::Instant::now();
    let mut overview = feed::derive::live::Overview::default();
    overview.refresh(feed::derive::live::fetch(&client)?);
    let w = overview.world();
    let needs = w.needs();
    println!("{:?}: {} sessions, {} projects, {} work items, {} needs, {} hosts, {} meters", t.elapsed(), w.sessions.len(), w.projects.len(), w.work.len(), needs.len(), w.hosts.len(), w.usage.len());
    Ok(())
}
