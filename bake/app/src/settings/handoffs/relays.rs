use rune_kit::Kit;
use feed::handoffs::{GOAL_COMPLETED, Handoffs, Relay, TURN_FINISHED};
use rune::{El, col, row, text};
use time::OffsetDateTime;

use super::super::parts::{self, line};
use super::super::{Prefs, Section, busy, saving};
use super::{Who, change, draft, edited, field, matches, note, remove, small, status};
use crate::theme::{self, BODY, META, attention, muted};

fn add(s: &mut Prefs) {
    let d = draft(s);
    if let Some(p) = d.problem() {
        return note(s, Section::Relays, Some(p));
    }
    if !busy(s) {
        change(s, Section::Relays, "relay-add".into(), move |c| c.relay_add(&d));
    }
}

fn relay(s: &Prefs, r: &Relay, now: OffsetDateTime, off: bool, k: &Kit) -> El {
    let mut words = col()
        .gap(2.0)
        .grow(1.0)
        .min_w(0.0)
        .child(text(r.route()).font(k.text(BODY)).color(theme::text()).wrap())
        .child(text(r.pattern.clone()).font(k.text(META)).color(muted()).wrap())
        .child(text(r.state(now)).font(k.text(META)).color(muted()).wrap());
    if let Some(problem) = r.problem() {
        words = words.child(text(problem.to_string()).font(k.text(META)).color(attention()).wrap());
    }
    let id = r.id;
    let key = format!("relay-remove-{id}");
    let button = remove(&key, off || saving(s, &key), move |s| change(s, Section::Relays, format!("relay-remove-{id}"), move |c| c.relay_remove(id)), k);
    row().w_full().gap(16.0).items_center().px(14.0).py(10.0).child(words).child(button)
}

pub fn section(s: &Prefs, h: &Handoffs, off: bool, layer: &mut Vec<El>, k: &Kit) -> El {
    let now = OffsetDateTime::now_utc();
    let f = &s.handoffs;
    let mut rows: Vec<El> = h.relays.iter().map(|r| relay(s, r, now, off, k)).collect();
    if rows.is_empty() {
        rows.push(parts::words("No automatic handoffs yet.", k));
    }
    let choices = vec![("Turn finished".to_string(), TURN_FINISHED.to_string()), ("Goal completed".to_string(), GOAL_COMPLETED.to_string())];
    let when = parts::select(s, "settings-relay-handoff", "Hand off when", choices, &f.relay_handoff, off, layer, |s, value| {
        s.handoffs.relay_handoff = value;
        edited(s, Section::Relays);
    }, k);
    let once = f.relay_once;
    rows.push(line("When", Some("The session whose replies hand off".into()), field("relay-from", Section::Relays, &f.relay_from, "When", "Find a session or @alias", off, Some(Who::From), |s| &mut s.handoffs.relay_from, add, k), k));
    rows.extend(matches(s, Who::From, off, k));
    rows.push(line("Hand off when", None, when, k));
    rows.push(line("Finishes with text matching", Some("A regular expression; .* matches every reply".into()), field("relay-pattern", Section::Relays, &f.relay_pattern, "Finishes with text matching", ".*", off, None, |s| &mut s.handoffs.relay_pattern, add, k), k));
    rows.push(line("Hand off to", None, field("relay-to", Section::Relays, &f.relay_to, "Hand off to", "Find a session or @alias", off, Some(Who::To), |s| &mut s.handoffs.relay_to, add, k), k));
    rows.extend(matches(s, Who::To, off, k));
    rows.push(line("Run this handoff once", None, parts::switch("relay-once", "Run this handoff once", once, off, move |s| {
        s.handoffs.relay_once = !once;
        edited(s, Section::Relays);
    }, k), k));
    rows.extend(status(s, Section::Relays, k));
    let ready = draft(s).problem().is_none();
    rows.push(row().w_full().px(14.0).py(10.0).child(col().grow(1.0)).child(small("Add Handoff", "relay-add", off || !ready, add, k)));
    parts::section(&format!("Automatic handoffs · {}", h.relays.len()), rows, Some("When one session finishes with matching text, Agenthail sends its reply to another.".into()), k)
}
