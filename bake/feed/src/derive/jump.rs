use crate::catalog::Session;

use super::needs::{Need, Target};
use super::phase::{CHECK, PHASES, phase};
use super::sessions::busy;
use super::text::{plain, short_id, short_title};
use super::world::{Work, World};

const PER_GROUP: usize = 3;

pub struct Hit {
    pub group: &'static str,
    pub title: String,
    pub status: String,
    pub badge: String,
    pub avatar: String,
    pub target: Target,
}

impl World {
    pub fn jump(&self, query: &str, needs: &[Need], hands_on: &[String]) -> Vec<Hit> {
        let q = query.trim().to_lowercase();
        let has = |t: &str| q.is_empty() || t.to_lowercase().contains(&q);
        let mut out = Vec::new();
        for p in self.projects.iter().filter(|p| has(&p.name)).take(PER_GROUP) {
            let waiting = needs.iter().filter(|n| n.project_key == p.key).count();
            let moving = p.sessions.iter().filter_map(|id| self.session(id)).filter(|x| busy(x)).count();
            let mut status = Vec::new();
            if waiting > 0 {
                status.push(format!("{waiting} need you"));
            }
            status.push(if moving > 0 { format!("{moving} moving") } else { "quiet".into() });
            status.push(if p.lead.is_some() { format!("lead {}", p.lead_name) } else { "no lead yet".into() });
            out.push(Hit { group: "Projects", title: p.name.clone(), status: status.join(" · "), badge: self.ago(&p.last_active), avatar: p.name.clone(), target: Target::Project(p.key.clone()) });
        }
        let mut work: Vec<&Work> = self.work.values().filter(|w| matches!(w.state.as_str(), "in_progress" | "in_review")).filter(|w| has(&w.text) || has(&w.id)).collect();
        work.sort_by(|a, b| phase(b).cmp(&phase(a)).then(b.last_at().cmp(a.last_at())).then(a.id.cmp(&b.id)));
        for w in work.into_iter().take(PER_GROUP) {
            let ph = phase(w);
            let whose = if ph == CHECK { "your move".to_string() } else { format!("{}'s move", short_title(&self.whose_move(w), 24)) };
            let project = self.project(&w.project).map(|p| p.name.clone()).unwrap_or_default();
            out.push(Hit { group: "Work", title: plain(&w.text), status: format!("{} · {whose} · {project}", PHASES[ph]), badge: short_id(&w.id), avatar: project, target: Target::Work(w.id.clone()) });
        }
        let mut sessions: Vec<&Session> = hands_on.iter().filter_map(|id| self.session(id)).collect();
        let mut rest: Vec<&Session> = self.sessions.iter().filter(|x| x.project.is_some() && !hands_on.contains(&x.id)).collect();
        rest.sort_by(|a, b| b.last_active.cmp(&a.last_active));
        sessions.extend(rest);
        for x in sessions.into_iter().filter(|x| has(&self.title(x)) || has(&x.project_name())).take(PER_GROUP) {
            let gist = self.gist(x);
            let state = if busy(x) { "Working" } else { "Idle" };
            let status = if gist == x.display.label { state.to_string() } else { format!("{state} · {}", short_title(&gist, 70)) };
            out.push(Hit { group: "Sessions", title: self.title(x), status, badge: x.project_name(), avatar: x.project_name(), target: Target::Session(x.id.clone()) });
        }
        out
    }
}
