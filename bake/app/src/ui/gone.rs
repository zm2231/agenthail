use rune_kit::Kit;
use super::*;
use feed::gone::{Fix, Gone, Lost};

const RETRY: f64 = 20.0;

static LOOKUPS: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

pub enum Look {
    Pending,
    Found(Gone),
    Deleted(String),
    Failed(String),
}

pub struct Missing {
    pub id: String,
    pub look: Look,
    asked: f64,
    asking: Option<u64>,
    pub fixing: Option<(Fix, u64)>,
    fresh: bool,
    stale: bool,
    pub error: Option<String>,
}

pub fn shown<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    session(s, id).or_else(|| match &s.missing {
        Some(Missing { id: m, look: Look::Found(g), .. }) if m == id => Some(&g.row),
        _ => None,
    })
}

fn not_found(id: String, why: String, at: f64) -> Missing {
    Missing { id, look: Look::Failed(why), asked: at, asking: None, fixing: None, fresh: false, stale: false, error: None }
}

fn due(m: &Missing, clock: f64) -> bool {
    if m.asking.is_some() || m.fixing.is_some() {
        return false;
    }
    if m.stale || m.asked == f64::NEG_INFINITY {
        return true;
    }
    match &m.look {
        Look::Found(g) => g.incomplete && crate::wake::due_clock(m.asked + RETRY, clock),
        Look::Deleted(_) => false,
        Look::Pending | Look::Failed(_) => crate::wake::due_clock(m.asked + RETRY, clock),
    }
}

pub fn watch(s: &mut App) -> bool {
    let Some(id) = s.selected.clone().filter(|_| s.screen == Screen::Session) else {
        return s.missing.take().is_some();
    };
    if let Some(x) = session(s, &id).filter(|x| !x.archived) {
        s.last_row = Some(x.clone());
        if s.missing.take().is_none() {
            return false;
        }
        super::inspect::returned(s, &id);
        super::session_menu::changed(s, &id);
        return true;
    }
    if super::session_menu::changing(s, &id) {
        return false;
    }
    let Some(client) = s.client.clone() else {
        if s.missing.as_ref().is_some_and(|m| m.id == id) {
            return false;
        }
        let why = if s.fixtures.is_some() { "This session isn't in the recording.".to_string() } else { s.link.problem.as_ref().map_or_else(|| format!("Agenthail isn't connected: {}", s.status), |p| p.detail()) };
        s.missing = Some(not_found(id, why, s.clock));
        return true;
    };
    if s.snapshots == 0 && !s.status.starts_with("daemon:") {
        return false;
    }
    if s.missing.as_ref().is_some_and(|m| m.id == id && !due(m, s.clock)) {
        return false;
    }
    let seq = LOOKUPS.fetch_add(1, std::sync::atomic::Ordering::Relaxed) + 1;
    match s.missing.as_mut().filter(|m| m.id == id) {
        Some(m) => {
            m.asked = s.clock;
            m.asking = Some(seq);
            m.stale = false;
        }
        None => s.missing = Some(Missing { id: id.clone(), look: Look::Pending, asked: s.clock, asking: Some(seq), fixing: None, fresh: false, stale: false, error: None }),
    }
    let known = s.last_row.clone().filter(|r| r.id == id).or_else(|| s.ov.d.session(&id).cloned());
    let tx = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.look_up(&id, known.as_ref());
        let _ = tx.send(Bg::Looked(id, seq, result));
    });
    true
}

pub fn looked(s: &mut App, id: &str, seq: u64, result: Result<Gone, Lost>) {
    let Some(m) = s.missing.as_mut().filter(|m| m.id == id && m.asking == Some(seq)) else { return };
    m.asking = None;
    m.fresh = true;
    m.look = match result {
        Ok(g) => Look::Found(g),
        Err(Lost::Deleted(e)) => Look::Deleted(e),
        Err(Lost::Failed(e)) => Look::Failed(e),
    };
}

pub fn settled(s: &App, id: &str) -> bool {
    s.missing.as_ref().is_none_or(|m| m.id != id || m.fresh)
}

pub fn changed(s: &mut App, id: &str) {
    if let Some(m) = s.missing.as_mut().filter(|m| m.id == id) {
        m.stale = true;
    }
}

pub fn recheck(s: &mut App, id: &str) {
    if let Some(m) = s.missing.as_mut().filter(|m| m.id == id) {
        m.fresh = false;
        m.asking = None;
        m.asked = f64::NEG_INFINITY;
    }
}

fn fix(s: &mut App, fix: Fix) {
    let Some(client) = s.client.clone() else { return };
    if s.missing.as_ref().is_none_or(|m| !m.fresh || m.fixing.is_some() || super::session_menu::changing(s, &m.id)) {
        return;
    }
    let Some(m) = s.missing.as_mut() else { return };
    s.menus.busy.insert((m.id.clone(), "menu"));
    let seq = LOOKUPS.fetch_add(1, std::sync::atomic::Ordering::Relaxed) + 1;
    m.fixing = Some((fix, seq));
    m.asking = None;
    m.error = None;
    let (id, tx) = (m.id.clone(), s.outbox.clone());
    std::thread::spawn(move || {
        let result = client.fix(&id, fix);
        let _ = tx.send(Bg::Fixed(id, seq, fix, result));
    });
}

pub fn fixed(s: &mut App, id: &str, seq: u64, fix: Fix, result: Result<(), feed::catalog::RespondError>) {
    s.menus.busy.remove(&(id.to_string(), "menu"));
    let Some(m) = s.missing.as_mut().filter(|m| m.id == id && m.fixing.is_some_and(|(_, n)| n == seq)) else { return };
    m.fixing = None;
    m.fresh = false;
    m.asked = f64::NEG_INFINITY;
    if let Err(e) = result {
        m.error = Some(if e.maybe_delivered { format!("{} may not have gone through: {}", fix.doing(), e.message) } else { format!("{} didn't work: {}", fix.doing(), e.message) });
    }
}

fn banner(m: &Missing, g: &Gone, k: &Kit) -> El {
    let mut text_col = col().grow(1.0).min_w(0.0).gap(3.0).child(text(g.note()).font(k.text(BODY_BOLD)).color(theme::text()).wrap());
    if let Some(e) = &g.read_error {
        text_col = text_col.child(text(format!("Couldn't read all of its conversation: {e}")).font(k.text(META)).color(attention()).wrap());
    }
    if let Some(e) = &m.error {
        text_col = text_col.child(text(e.clone()).font(k.text(META)).color(failed()).wrap());
    }
    let mut r = row()
        .key("gone-note")
        .w_full()
        .gap(12.0)
        .items_center()
        .px(14.0)
        .py(10.0)
        .radius(12.0)
        .fill(attention().alpha(0.10))
        .border(1.0, attention().alpha(0.25))
        .child(icon(&icons::CLOCK, k.text_size(16.0)).color(attention()))
        .child(text_col);
    if let Some(f) = g.fix {
        let label = match (m.fixing, m.fresh) {
            (Some(_), _) => format!("{}…", f.doing()),
            (None, false) => "Checking…".to_string(),
            (None, true) => f.label().to_string(),
        };
        r = r.child(Button::new(label).variant(ButtonVariant::Ghost).build(k).key("gone-fix").on_press(move |s: &mut App| self::fix(s, f)));
    }
    col().w_full().px(32.0).pb(12.0).child(r)
}

fn failed_view(message: &str, k: &Kit) -> El {
    col()
        .key("gone-failed")
        .w_full()
        .grow(1.0)
        .items_center()
        .justify_center()
        .gap(12.0)
        .child(text(message.to_string()).font(k.text(BODY_BOLD)).color(muted()).wrap())
        .child(Button::new("Go to Now").variant(ButtonVariant::Ghost).build(k).key("gone-now").on_press(|s: &mut App| crate::over::go(s, Screen::Now)))
        .child(el().h(80.0))
}

fn exchanges(g: &Gone, k: &Kit) -> El {
    let turn = |who: &str, body: &str, color: Color| col().w_full().gap(4.0).child(label(who, k)).child(text(body.to_string()).font(k.text(BODY)).color(color).wrap());
    let mut head = row().w_full().gap(10.0).items_center();
    head = head.child(text(g.history.note()).font(k.text(META)).color(faint()));
    let list = col().w_full().max_w(780.0).gap(18.0).px(32.0).py(16.0).child(head).children(g.exchanges.iter().map(|e| {
        let mut c = col().w_full().gap(10.0);
        if !e.user.is_empty() {
            c = c.child(turn(&e.from, &e.user, theme::text()));
        }
        if !e.assistant.is_empty() {
            c = c.child(turn("Agent", &e.assistant, muted()));
        }
        c
    }));
    col().key("gone-exchanges").w_full().grow(1.0).min_h(0.0).items_center().scroll(ScrollSpec { stick_bottom: true }).child(list)
}

pub fn screen(s: &App, id: &str, k: &Kit) -> El {
    let Some(m) = s.missing.as_ref().filter(|m| m.id == id) else { return transcript::loading("Loading sessions…".into(), k) };
    match &m.look {
        Look::Pending => transcript::loading("Looking for this session…".into(), k),
        Look::Deleted(e) | Look::Failed(e) => failed_view(e, k),
        Look::Found(g) => {
            let center = col().grow(1.0).h_full().min_w(0.0).child(header(s, &g.row, k)).child(banner(m, g, k));
            if !g.exchanges.is_empty() {
                center.child(exchanges(g, k))
            } else if s.panes.get(id).is_some_and(|p| !p.blocks.is_empty()) {
                conversation(s, id, center, k)
            } else {
                center.child(col().key("gone-empty").w_full().grow(1.0).items_center().justify_center().child(text(g.empty_note()).font(k.text(BODY_BOLD)).color(faint()).wrap()).child(el().h(80.0)))
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn missing(look: Look, asked: f64) -> Missing {
        Missing { id: "s1".into(), look, asked, asking: None, fixing: None, fresh: true, stale: false, error: None }
    }

    #[test]
    fn an_answered_lookup_waits_for_a_change_and_a_failed_one_retries() {
        let mut m = missing(Look::Deleted("gone".into()), 0.0);
        assert!(!due(&m, 1000.0), "an answer isn't looked up again on a timer");
        m.stale = true;
        assert!(due(&m, 1000.0), "a change to the session looks it up again");
        m.stale = false;
        m.asked = f64::NEG_INFINITY;
        assert!(due(&m, 0.0), "an action on it looks it up again");
        let mut f = missing(Look::Failed("offline".into()), 0.0);
        assert!(!due(&f, RETRY - 1.0));
        assert!(due(&f, RETRY));
        f.stale = true;
        assert!(due(&f, 1.0), "a change after a failed lookup looks it up again at once");
        f.asking = Some(1);
        assert!(!due(&f, RETRY), "one lookup at a time");
        let gone = |incomplete| Gone { row: Session::default(), why: feed::gone::Why::Unlisted, fix: None, exchanges: Vec::new(), read_error: None, history: Default::default(), incomplete };
        assert!(!due(&missing(Look::Found(gone(false)), 0.0), RETRY));
        let partial = missing(Look::Found(gone(true)), 0.0);
        assert!(!due(&partial, RETRY - 1.0));
        assert!(due(&partial, RETRY), "a lookup that failed part way tries again");
    }
}
