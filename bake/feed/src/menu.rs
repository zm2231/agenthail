use crate::action::ReadState;
use crate::catalog::Session;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Act {
    Rename,
    Terminal,
    Pr(String),
    Pin(bool),
    Read(bool),
    Archive(bool),
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Entry {
    pub act: Act,
    pub label: String,
    pub note: Option<String>,
    pub enabled: bool,
}

impl Entry {
    fn on(act: Act, label: impl Into<String>) -> Entry {
        Entry { act, label: label.into(), note: None, enabled: true }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum Patch {
    Pinned(Option<String>),
    Archived(bool),
    Read { receipt: ReadState, from: String },
    Alias(Option<String>),
}

impl Patch {
    pub fn apply(&self, x: &mut Session) {
        match self {
            Patch::Pinned(at) => x.pinned_at = at.clone(),
            Patch::Archived(on) => x.archived = *on,
            Patch::Read { receipt, .. } => x.read_state = Some(receipt.clone()),
            Patch::Alias(alias) => x.alias = alias.clone(),
        }
    }

    /// A read receipt holds while the row still carries the revision it replaced.
    pub fn shown(&self, x: &Session) -> bool {
        match self {
            Patch::Pinned(at) => x.pinned_at.is_some() == at.is_some(),
            Patch::Archived(on) => x.archived == *on,
            Patch::Read { from, .. } => x.read_state.as_ref().is_none_or(|r| r.revision != *from),
            Patch::Alias(alias) => x.alias == *alias,
        }
    }

    pub fn same_field(&self, other: &Patch) -> bool {
        std::mem::discriminant(self) == std::mem::discriminant(other)
    }

    pub fn same_target(&self, other: &Patch) -> bool {
        match (self, other) {
            (Patch::Pinned(a), Patch::Pinned(b)) => a.is_some() == b.is_some(),
            (Patch::Read { receipt: a, .. }, Patch::Read { receipt: b, .. }) => a.revision == b.revision,
            _ => self == other,
        }
    }

    pub fn restore(&self, x: &mut Session, daemon: &Session) {
        match self {
            Patch::Pinned(_) => x.pinned_at = daemon.pinned_at.clone(),
            Patch::Archived(_) => x.archived = daemon.archived,
            Patch::Read { .. } => x.read_state = daemon.read_state.clone(),
            Patch::Alias(_) => x.alias = daemon.alias.clone(),
        }
    }
}

impl Session {
    pub fn pinned(&self) -> bool {
        self.pinned_at.is_some()
    }

    pub fn unread(&self) -> bool {
        self.read_state.as_ref().is_some_and(|r| !r.first_unread.is_empty())
    }

    pub fn local(&self) -> bool {
        matches!(self.host.state.as_str(), "" | "local")
    }

    pub fn focusable(&self) -> bool {
        self.runtime.as_ref().is_some_and(|r| r.focusable)
    }
}

/// The daemon refuses session-focus for a session on another host.
pub fn entries(x: &Session) -> Vec<Vec<Entry>> {
    let mut open = vec![Entry::on(Act::Rename, "Rename…")];
    if x.focusable() {
        let mut terminal = Entry::on(Act::Terminal, "Open in terminal");
        if !x.local() {
            terminal.enabled = false;
            terminal.note = Some(format!("Runs on {}", x.host.name));
        }
        open.push(terminal);
    }
    for pr in x.prs.iter().filter(|p| !p.url.is_empty()) {
        let label = if x.prs.len() == 1 { "Open pull request".to_string() } else { format!("Open pull request #{}", pr.number) };
        let mut entry = Entry::on(Act::Pr(pr.url.clone()), label);
        if !pr.title.is_empty() {
            entry.note = Some(pr.title.clone());
        }
        open.push(entry);
    }
    let mut keep = vec![Entry::on(Act::Pin(!x.pinned()), if x.pinned() { "Unpin" } else { "Pin" })];
    if x.read_state.is_some() {
        keep.push(Entry::on(Act::Read(x.unread()), if x.unread() { "Mark as read" } else { "Mark as unread" }));
    }
    keep.push(Entry::on(Act::Archive(!x.archived), if x.archived { "Unarchive" } else { "Archive" }));
    vec![open, keep]
}

pub fn normalized(text: &str) -> String {
    let trimmed = text.trim();
    trimmed.strip_prefix('@').unwrap_or(trimmed).to_string()
}

pub fn clears(text: &str, current: Option<&str>) -> bool {
    normalized(text).is_empty() && !current.unwrap_or_default().is_empty()
}

pub fn rename_problem(text: &str, current: Option<&str>) -> Option<&'static str> {
    if clears(text, current) {
        return None;
    }
    let handle = normalized(text);
    if handle.is_empty() {
        return Some("Enter a name.");
    }
    if handle.len() > 80 {
        return Some("That name is too long.");
    }
    if handle.chars().any(|c| c.is_whitespace() || c == '/' || c == '#') {
        return Some("Names can't contain spaces, /, or #.");
    }
    None
}

pub fn unchanged(text: &str, current: Option<&str>) -> bool {
    normalized(text) == current.unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Named, Pr, Runtime};

    fn session() -> Session {
        Session { id: "s1".into(), host: Named { name: "mac".into(), state: "local".into(), ..Default::default() }, ..Default::default() }
    }

    fn labels(x: &Session) -> Vec<Vec<String>> {
        entries(x).into_iter().map(|g| g.into_iter().map(|e| e.label).collect()).collect()
    }

    #[test]
    fn a_plain_session_offers_rename_pin_and_archive() {
        assert_eq!(labels(&session()), vec![vec!["Rename…"], vec!["Pin", "Archive"]]);
    }

    #[test]
    fn each_toggle_names_what_it_will_do() {
        let mut x = session();
        x.pinned_at = Some("2026-11-26T10:00:00Z".into());
        x.archived = true;
        x.read_state = Some(ReadState { first_unread: "item-4".into(), revision: "r1".into() });
        let keep = &entries(&x)[1];
        assert_eq!(keep.iter().map(|e| (e.label.as_str(), e.act.clone())).collect::<Vec<_>>(), vec![("Unpin", Act::Pin(false)), ("Mark as read", Act::Read(true)), ("Unarchive", Act::Archive(false))]);
        x.read_state = Some(ReadState { first_unread: String::new(), revision: "r1".into() });
        assert_eq!(entries(&x)[1][1], Entry::on(Act::Read(false), "Mark as unread"));
    }

    #[test]
    fn read_state_is_offered_only_where_the_session_keeps_one() {
        assert!(!entries(&session()).concat().iter().any(|e| matches!(e.act, Act::Read(_))));
    }

    #[test]
    fn the_terminal_opens_only_for_a_focusable_session_on_this_computer() {
        let mut x = session();
        assert!(!entries(&x).concat().iter().any(|e| e.act == Act::Terminal));
        x.runtime = Some(Runtime { focusable: true });
        let terminal = entries(&x).concat().into_iter().find(|e| e.act == Act::Terminal).unwrap();
        assert!(terminal.enabled);
        x.host = Named { name: "forge".into(), state: "connected".into(), ..Default::default() };
        let terminal = entries(&x).concat().into_iter().find(|e| e.act == Act::Terminal).unwrap();
        assert!(!terminal.enabled);
        assert_eq!(terminal.note.as_deref(), Some("Runs on forge"));
    }

    #[test]
    fn a_session_with_no_host_reported_is_on_this_computer() {
        let mut x = session();
        x.host = Named::default();
        assert!(x.local());
    }

    #[test]
    fn each_pull_request_with_a_link_gets_an_entry() {
        let mut x = session();
        x.prs = vec![Pr { number: 12, title: "Fix it".into(), url: "https://github.com/o/r/pull/12".into() }];
        let open = &entries(&x)[0];
        assert_eq!(open[1].label, "Open pull request");
        assert_eq!(open[1].act, Act::Pr("https://github.com/o/r/pull/12".into()));
        assert_eq!(open[1].note.as_deref(), Some("Fix it"));
        x.prs.push(Pr { number: 13, title: String::new(), url: "https://github.com/o/r/pull/13".into() });
        x.prs.push(Pr { number: 14, title: "No link".into(), url: String::new() });
        assert_eq!(labels(&x)[0], vec!["Rename…", "Open pull request #12", "Open pull request #13"]);
    }

    #[test]
    fn rename_refuses_what_the_daemon_refuses() {
        assert_eq!(rename_problem("", None), Some("Enter a name."));
        assert_eq!(rename_problem("  ", Some("scout")), None);
        assert!(clears("@", Some("scout")));
        assert_eq!(rename_problem("two words", None), Some("Names can't contain spaces, /, or #."));
        assert_eq!(rename_problem("a/b", None), Some("Names can't contain spaces, /, or #."));
        assert_eq!(rename_problem("a#b", None), Some("Names can't contain spaces, /, or #."));
        assert_eq!(rename_problem(&"x".repeat(81), None), Some("That name is too long."));
        assert_eq!(rename_problem(&"x".repeat(80), None), None);
        assert_eq!(rename_problem(" @scout ", None), None);
        assert_eq!(normalized(" @scout "), "scout");
        assert!(unchanged("@scout", Some("scout")));
        assert!(unchanged("", None));
        assert!(!unchanged("scout2", Some("scout")));
    }

    #[test]
    fn a_landed_change_shows_on_the_session() {
        let mut x = session();
        Patch::Pinned(Some("2026-11-26T10:00:00Z".into())).apply(&mut x);
        Patch::Archived(true).apply(&mut x);
        Patch::Alias(Some("scout".into())).apply(&mut x);
        Patch::Read { receipt: ReadState { first_unread: "item-2".into(), revision: "r2".into() }, from: "r1".into() }.apply(&mut x);
        assert!(x.pinned() && x.archived && x.unread());
        assert_eq!(x.title(), "@scout");
        Patch::Pinned(None).apply(&mut x);
        Patch::Alias(None).apply(&mut x);
        assert!(!x.pinned());
        assert_eq!(x.alias, None);
    }

    #[test]
    fn a_read_receipt_holds_until_the_row_moves_past_the_revision_it_replaced() {
        let mut x = session();
        let read = Patch::Read { receipt: ReadState { first_unread: String::new(), revision: "B".into() }, from: "A".into() };
        x.read_state = Some(ReadState { first_unread: "item-1".into(), revision: "A".into() });
        assert!(!read.shown(&x), "the row is still at the revision the mark replaced");
        x.read_state = Some(ReadState { first_unread: String::new(), revision: "B".into() });
        assert!(read.shown(&x));
        x.read_state = Some(ReadState { first_unread: "item-7".into(), revision: "C".into() });
        assert!(read.shown(&x), "a later revision is the daemon's word");
    }

    #[test]
    fn pinned_sessions_stay_in_the_sidebar() {
        let now = time::OffsetDateTime::now_utc();
        let mut x = session();
        x.last_active = "2020-01-01T00:00:00Z".into();
        assert!(!x.visible(now));
        x.pinned_at = Some("2026-11-26T10:00:00Z".into());
        assert!(x.visible(now));
        x.archived = true;
        assert!(!x.visible(now));
    }
}
