use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

pub const KEPT_CALLS: usize = 20;
pub const UNFINISHED: &str = "process_terminated_or_unknown";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Setup {
    Window,
    Page,
    Check,
}

impl Setup {
    pub fn limit(self) -> f64 {
        match self {
            Setup::Window => 3.0,
            Setup::Page => 15.0,
            Setup::Check => 5.0,
        }
    }

    pub fn waiting(self) -> &'static str {
        match self {
            Setup::Window => "Opening call audio…",
            Setup::Page => "Loading the audio page…",
            Setup::Check => "Checking the audio page…",
        }
    }

    pub fn stuck(self) -> &'static str {
        match self {
            Setup::Window => "Call audio can't start: its hidden view never joined this window.",
            Setup::Page => "Call audio can't start: the audio page hasn't finished loading after 15 seconds.",
            Setup::Check => "Call audio can't start: the audio page loaded but didn't answer its readiness check.",
        }
    }
}

#[derive(Serialize, Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Attempt {
    pub id: String,
    #[serde(default)]
    pub started_ms: i64,
    #[serde(default)]
    pub milestones: Vec<(String, f64)>,
    pub cause: Option<String>,
    pub failed_at: Option<String>,
    #[serde(default)]
    pub owner: u32,
    #[serde(skip)]
    clock: f64,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Trail {
    pub attempts: Vec<Attempt>,
    pub changed: bool,
    own: Vec<String>,
    pid: u32,
}

impl Trail {
    pub fn owned_by(pid: u32) -> Trail {
        Trail { pid, ..Trail::default() }
    }

    pub fn parse(saved: &str) -> Result<Trail, String> {
        let attempts: Vec<Attempt> = serde_json::from_str(saved).map_err(|e| e.to_string())?;
        Ok(Trail { attempts, ..Trail::default() })
    }

    pub fn load(saved: &str, pid: u32, alive: impl Fn(u32) -> bool) -> Result<Trail, String> {
        let mut trail = Trail { pid, ..Trail::parse(saved)? };
        let closed = trail.close_orphans(&alive);
        let trimmed = trail.cap();
        trail.changed = closed || trimmed;
        Ok(trail)
    }

    fn close_orphans(&mut self, alive: &impl Fn(u32) -> bool) -> bool {
        let (pid, own) = (self.pid, &self.own);
        let mut closed = false;
        for attempt in self.attempts.iter_mut().filter(|a| a.cause.is_none() && !own.contains(&a.id)) {
            if attempt.owner == 0 || attempt.owner == pid || !alive(attempt.owner) {
                attempt.cause = Some(UNFINISHED.into());
                closed = true;
            }
        }
        closed
    }

    pub fn combine(&mut self, saved: Trail, alive: impl Fn(u32) -> bool) {
        let own = std::mem::take(&mut self.own);
        let mut attempts: Vec<Attempt> = saved.attempts.into_iter().filter(|a| !own.contains(&a.id)).collect();
        attempts.extend(self.attempts.drain(..).filter(|a| own.contains(&a.id)));
        attempts.sort_by_key(|a| (a.started_ms, a.owner));
        self.attempts = attempts;
        self.own = own;
        self.close_orphans(&alive);
        self.cap();
    }

    fn cap(&mut self) -> bool {
        let before = self.attempts.len();
        let mut extra = before.saturating_sub(KEPT_CALLS);
        self.attempts.retain(|a| {
            let drop = extra > 0 && a.cause.is_some();
            extra -= usize::from(drop);
            !drop
        });
        let attempts = &self.attempts;
        self.own.retain(|id| attempts.iter().any(|a| a.id == *id));
        self.attempts.len() < before
    }

    pub fn save(&self) -> String {
        serde_json::to_string(&self.attempts).unwrap_or_else(|_| "[]".into())
    }

    pub fn last(&self) -> Option<&Attempt> {
        self.attempts.iter().rev().find(|a| self.own.contains(&a.id)).or(self.attempts.last())
    }

    pub fn begin(&mut self, id: &str, clock: f64) {
        let started_ms = i64::try_from(OffsetDateTime::now_utc().unix_timestamp_nanos() / 1_000_000).unwrap_or(i64::MAX);
        self.attempts.push(Attempt { id: id.into(), started_ms, owner: self.pid, clock, ..Attempt::default() });
        self.own.push(id.into());
        self.cap();
        self.changed = true;
    }

    fn open(&mut self, id: &str) -> Option<&mut Attempt> {
        self.attempts.iter_mut().rev().find(|a| a.id == id && a.cause.is_none())
    }

    pub fn mark(&mut self, id: &str, name: &str, clock: f64) {
        let Some(attempt) = self.open(id) else { return };
        if attempt.milestones.iter().any(|(n, _)| n == name) {
            return;
        }
        let at = (clock - attempt.clock).max(0.0);
        attempt.milestones.push((name.into(), at));
        self.changed = true;
    }

    pub fn fail(&mut self, id: &str, stage: &str) {
        let Some(attempt) = self.open(id) else { return };
        if attempt.failed_at.is_none() {
            attempt.failed_at = Some(stage.into());
            self.changed = true;
        }
    }

    pub fn end(&mut self, id: &str, cause: &str, clock: f64) {
        self.mark(id, "local_ended", clock);
        let Some(attempt) = self.open(id) else { return };
        attempt.cause = Some(cause.into());
        self.changed = true;
    }
}
