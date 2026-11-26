use super::*;
use feed::gone::{Fix, Gone};

const RECHECK: f64 = 20.0;

pub enum Look {
    Pending,
    Found(Gone),
    Failed(String),
}

pub struct Missing {
    pub id: String,
    pub look: Look,
    asked: f64,
    asking: bool,
    pub fixing: Option<Fix>,
    pub error: Option<String>,
}

pub fn shown<'a>(s: &'a App, id: &str) -> Option<&'a Session> {
    session(s, id).or_else(|| match &s.missing {
        Some(Missing { id: m, look: Look::Found(g), .. }) if m == id => Some(&g.row),
        _ => None,
    })
}

pub fn watch(s: &mut App) {
    let Some(id) = s.selected.clone().filter(|_| s.screen == Screen::Session) else {
        s.missing = None;
        return;
    };
    if let Some(x) = session(s, &id) {
        s.last_row = Some(x.clone());
        s.missing = None;
        return;
    }
    if s.menus.busy.contains(&(id.clone(), "menu")) {
        return;
    }
    let Some(client) = s.client.clone() else {
        if s.missing.as_ref().is_none_or(|m| m.id != id) {
            s.missing = Some(Missing { id, look: Look::Failed("This session isn't in the recording.".into()), asked: s.clock, asking: false, fixing: None, error: None });
        }
        return;
    };
    if s.snapshots == 0 && !s.status.starts_with("daemon:") {
        return;
    }
    let mine = s.missing.as_mut().filter(|m| m.id == id);
    match mine {
        Some(m) if m.asking || m.fixing.is_some() || s.clock - m.asked < RECHECK => return,
        Some(m) => {
            m.asked = s.clock;
            m.asking = true;
        }
        None => s.missing = Some(Missing { id: id.clone(), look: Look::Pending, asked: s.clock, asking: true, fixing: None, error: None }),
    }
    let known = s.last_row.clone().filter(|r| r.id == id).or_else(|| s.ov.d.session(&id).cloned());
    let tx = s.outbox.clone();
    std::thread::spawn(move || {
        let result = client.look_up(&id, known.as_ref());
        let _ = tx.send(Bg::Looked(id, result));
    });
}

pub fn looked(s: &mut App, id: &str, result: Result<Gone, String>) {
    let Some(m) = s.missing.as_mut().filter(|m| m.id == id) else { return };
    m.asking = false;
    m.look = match result {
        Ok(g) => Look::Found(g),
        Err(e) => Look::Failed(e),
    };
}

fn fix(s: &mut App, fix: Fix) {
    let Some(client) = s.client.clone() else { return };
    let Some(m) = s.missing.as_mut().filter(|m| m.fixing.is_none()) else { return };
    m.fixing = Some(fix);
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

pub fn screen(s: &App, id: &str) -> El {
    let Some(m) = s.missing.as_ref().filter(|m| m.id == id) else { return transcript::loading("Loading sessions…".into()) };
    match &m.look {
        Look::Pending => transcript::loading("Looking for this session…".into()),
        Look::Failed(e) => failed_view(e),
        Look::Found(g) => {
            let center = col().grow(1.0).h_full().min_w(0.0).child(header(s, &g.row)).child(banner(m, g));
            conversation(s, id, center)
        }
    }
}
