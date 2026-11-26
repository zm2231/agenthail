use super::*;
use feed::gone::{Fix, Gone};

const RECHECK: f64 = 20.0;

static LOOKUPS: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

pub enum Look {
    Pending,
    Found(Gone),
    Failed(String),
}

pub struct Missing {
    pub id: String,
    pub look: Look,
    asked: f64,
    asking: Option<u64>,
    pub fixing: Option<Fix>,
    pub error: Option<String>,
}

pub fn shown<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    session(s, id).or_else(|| match &s.missing {
        Some(Missing { id: m, look: Look::Found(g), .. }) if m == id => Some(&g.row),
        _ => None,
    })
}

fn not_found(id: String, why: String, at: f64) -> Missing {
    Missing { id, look: Look::Failed(why), asked: at, asking: None, fixing: None, error: None }
}

pub fn watch(s: &mut App) -> bool {
    let Some(id) = s.selected.clone().filter(|_| s.screen == Screen::Session) else {
        return s.missing.take().is_some();
    };
    if let Some(x) = session(s, &id).filter(|x| !x.archived) {
        s.last_row = Some(x.clone());
        return s.missing.take().is_some();
    }
    if s.menus.busy.contains(&(id.clone(), "menu")) {
        return false;
    }
    let Some(client) = s.client.clone() else {
        if s.missing.as_ref().is_some_and(|m| m.id == id) {
            return false;
        }
        let why = if s.fixtures.is_some() { "This session isn't in the recording.".to_string() } else { format!("Agenthail isn't connected: {}", s.status) };
        s.missing = Some(not_found(id, why, s.clock));
        return true;
    };
    if s.snapshots == 0 && !s.status.starts_with("daemon:") {
        return false;
    }
    if s.missing.as_ref().is_some_and(|m| m.id == id && (m.asking.is_some() || m.fixing.is_some() || s.clock - m.asked < RECHECK)) {
        return false;
    }
    let seq = LOOKUPS.fetch_add(1, std::sync::atomic::Ordering::Relaxed) + 1;
    match s.missing.as_mut().filter(|m| m.id == id) {
        Some(m) => {
            m.asked = s.clock;
            m.asking = Some(seq);
        }
        None => s.missing = Some(Missing { id: id.clone(), look: Look::Pending, asked: s.clock, asking: Some(seq), fixing: None, error: None }),
    }
    let known = s.last_row.clone().filter(|r| r.id == id).or_else(|| s.ov.d.session(&id).cloned());
    let tx = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.look_up(&id, known.as_ref());
        let _ = tx.send(Bg::Looked(id, seq, result));
    });
    true
}

pub fn looked(s: &mut App, id: &str, seq: u64, result: Result<Gone, String>) {
    let Some(m) = s.missing.as_mut().filter(|m| m.id == id && m.asking == Some(seq)) else { return };
    m.asking = None;
    m.look = match result {
        Ok(g) => Look::Found(g),
        Err(e) => Look::Failed(e),
    };
}

pub fn recheck(s: &mut App, id: &str) {
    if let Some(m) = s.missing.as_mut().filter(|m| m.id == id) {
        m.asking = None;
        m.asked = f64::NEG_INFINITY;
    }
}

fn fix(s: &mut App, fix: Fix) {
    let Some(client) = s.client.clone() else { return };
    let Some(m) = s.missing.as_mut().filter(|m| m.fixing.is_none()) else { return };
    m.fixing = Some(fix);
    m.asking = None;
    m.error = None;
    let (id, tx) = (m.id.clone(), s.outbox.clone());
    std::thread::spawn(move || {
        let result = client.fix(&id, fix);
        let _ = tx.send(Bg::Fixed(id, fix, result));
    });
}

pub fn fixed(s: &mut App, id: &str, fix: Fix, result: Result<(), feed::catalog::RespondError>) {
    let Some(m) = s.missing.as_mut().filter(|m| m.id == id) else { return };
    m.fixing = None;
    m.asked = f64::NEG_INFINITY;
    if let Err(e) = result {
        m.error = Some(if e.maybe_delivered { format!("{} may not have gone through: {}", fix.doing(), e.message) } else { format!("{} didn't work: {}", fix.doing(), e.message) });
    }
}

fn banner(m: &Missing, g: &Gone) -> El {
    let k = theme::kit();
    let mut text_col = col().grow(1.0).min_w(0.0).gap(3.0).child(text(g.note()).font(BODY_BOLD).color(theme::text()).wrap());
    if let Some(e) = &m.error {
        text_col = text_col.child(text(e.clone()).font(META).color(failed()).wrap());
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
        .child(icon(&icons::CLOCK, 16.0).color(attention()))
        .child(text_col);
    if let Some(f) = g.fix {
        let label = match m.fixing {
            Some(_) => format!("{}…", f.doing()),
            None => f.label().to_string(),
        };
        r = r.child(Button::new(label).variant(ButtonVariant::Ghost).build(k).key("gone-fix").on_press(move |s: &mut App| self::fix(s, f)));
    }
    col().w_full().px(32.0).pb(12.0).child(r)
}

fn failed_view(message: &str) -> El {
    let k = theme::kit();
    col()
        .key("gone-failed")
        .w_full()
        .grow(1.0)
        .items_center()
        .justify_center()
        .gap(12.0)
        .child(text(message.to_string()).font(BODY_BOLD).color(muted()).wrap())
        .child(Button::new("Go to Now").variant(ButtonVariant::Ghost).build(k).key("gone-now").on_press(|s: &mut App| crate::over::go(s, Screen::Now)))
        .child(el().h(80.0))
}

fn exchanges(g: &Gone) -> El {
    let turn = |who: &str, body: &str, color: Color| col().w_full().gap(4.0).child(label(who)).child(text(body.to_string()).font(BODY).color(color).wrap());
    let mut head = row().w_full().gap(10.0).items_center();
    head = head.child(text(g.history.note()).font(META).color(faint()));
    let error = g.read_error.as_ref().map(|e| text(format!("Couldn't read all of this conversation: {e}")).font(META).color(failed()).wrap());
    let list = col().w_full().max_w(780.0).gap(18.0).px(32.0).py(16.0).child(head).children(error).children(g.exchanges.iter().map(|e| {
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

pub fn screen(s: &App, id: &str) -> El {
    let Some(m) = s.missing.as_ref().filter(|m| m.id == id) else { return transcript::loading("Loading sessions…".into()) };
    match &m.look {
        Look::Pending => transcript::loading("Looking for this session…".into()),
        Look::Failed(e) => failed_view(e),
        Look::Found(g) => {
            let center = col().grow(1.0).h_full().min_w(0.0).child(header(s, &g.row)).child(banner(m, g));
            let pane = s.panes.get(id).filter(|p| !p.blocks.is_empty());
            let live = pane.is_some_and(|p| matches!(p.status.as_str(), "live" | "replay" | "replay finished"));
            if live || (pane.is_some() && g.exchanges.is_empty()) {
                conversation(s, id, center)
            } else if g.exchanges.is_empty() {
                center.child(col().key("gone-empty").w_full().grow(1.0).items_center().justify_center().child(text(g.empty_note()).font(BODY_BOLD).color(faint()).wrap()).child(el().h(80.0)))
            } else {
                center.child(exchanges(g))
            }
        }
    }
}
