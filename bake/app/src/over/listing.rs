use std::collections::HashSet;
use std::sync::mpsc::Receiver;
use std::time::{Duration, Instant};

use feed::listing::{Listing, Page, RECENT, Request, SCOPES};
use feed::wake::Sender;
use feed::watch::Removal;

use crate::{App, Screen};
use crate::wake::channel;

const SETTLE: Duration = Duration::from_millis(300);
const TYPING: Duration = Duration::from_millis(250);
const SOONEST: Duration = Duration::from_secs(1);

#[derive(Clone, Debug, PartialEq)]
enum Read {
    First,
    More,
    Again,
    Fill(String),
}

enum Got {
    Page(Page),
    Whole(Listing),
}

type Reply = (u64, Read, Request, Result<Got, String>);

pub struct State {
    pub scope: usize,
    pub status: Option<String>,
    pub surface: Option<String>,
    pub status_open: bool,
    pub surface_open: bool,
    pub expanded: HashSet<String>,
    pub listing: Option<Listing>,
    pub error: Option<String>,
    surfaces: Vec<String>,
    projects: Vec<(String, String)>,
    shown: Option<Request>,
    failed: Option<Request>,
    filled: HashSet<String>,
    asked: Option<(u64, Read, Request)>,
    typed: Option<(Request, Instant)>,
    due: Option<Instant>,
    stale: bool,
    visible: bool,
    seq: u64,
    tx: Sender<Reply>,
    rx: Receiver<Reply>,
}

impl Default for State {
    fn default() -> Self {
        let (tx, rx) = channel();
        State {
            scope: RECENT,
            status: None,
            surface: None,
            status_open: false,
            surface_open: false,
            expanded: HashSet::new(),
            listing: None,
            error: None,
            surfaces: Vec::new(),
            projects: Vec::new(),
            shown: None,
            failed: None,
            filled: HashSet::new(),
            asked: None,
            typed: None,
            due: None,
            stale: false,
            visible: false,
            seq: 0,
            tx,
            rx,
        }
    }
}

impl State {
    pub fn loading(&self) -> bool {
        self.asked.is_some()
    }

    pub fn loading_more(&self) -> bool {
        self.asked.as_ref().is_some_and(|a| a.1 == Read::More)
    }

    pub fn surfaces(&self) -> &[String] {
        &self.surfaces
    }

    pub fn projects(&self) -> &[(String, String)] {
        &self.projects
    }

    pub fn clear_refinement(&mut self) {
        self.status = None;
        self.surface = None;
    }
}

pub fn request(s: &App) -> Request {
    Request {
        scope: SCOPES[s.ov.list.scope.min(SCOPES.len() - 1)].1.to_string(),
        project: s.ov.sessions_project.clone(),
        surface: s.ov.list.surface.clone(),
        status: s.ov.list.status.clone(),
        query: s.ov.sessions_query.text.trim().to_string(),
    }
}

fn ask(s: &mut App, read: Read, request: Request) {
    let Some(client) = s.client.clone() else { return };
    let st = &mut s.ov.list;
    st.seq += 1;
    if matches!(read, Read::First | Read::Again) {
        st.due = None;
        st.stale = false;
    }
    st.typed = None;
    st.failed = None;
    st.error = None;
    let (seq, tx) = (st.seq, st.tx.clone());
    let (cursor, families) = (st.listing.as_ref().and_then(|l| l.next.clone()), st.listing.as_ref().map_or(0, Listing::families));
    st.asked = Some((seq, read.clone(), request.clone()));
    std::thread::spawn(move || {
        let got = match &read {
            Read::First => client.listing_page(&request, None).map(Got::Page),
            Read::More => client.listing_page(&request, cursor.as_deref()).map(Got::Page),
            Read::Again => client.listing_reread(&request, families).map(Got::Whole),
            Read::Fill(project) => client.listing_project(&request, project).map(Got::Page),
        };
        let _ = tx.send((seq, read, request, got));
    });
}

pub fn more(s: &mut App) {
    let st = &s.ov.list;
    if st.asked.is_some() || st.listing.as_ref().is_none_or(|l| l.next.is_none()) {
        return;
    }
    let Some(request) = st.shown.clone() else { return };
    ask(s, Read::More, request);
}

#[cfg(test)]
pub fn show(s: &mut App, listing: Listing) {
    s.ov.list.shown = Some(request(s));
    s.ov.list.listing = Some(listing);
}

pub fn current(s: &App) -> bool {
    s.ov.list.shown.as_ref() == Some(&request(s))
}

pub fn retry(s: &mut App) {
    let want = request(s);
    let read = if s.ov.list.shown.as_ref() == Some(&want) && s.ov.list.listing.is_some() { Read::Again } else { Read::First };
    ask(s, read, want);
}

fn soon(s: &mut App, after: Duration) {
    let st = &mut s.ov.list;
    if st.listing.is_none() && st.asked.is_none() {
        return;
    }
    if !st.visible {
        st.stale = true;
        return;
    }
    let at = Instant::now() + after;
    st.due = Some(st.due.map_or(at, |d| d.min(at)));
}

pub fn news(s: &mut App) {
    soon(s, SETTLE);
}

pub fn reopened(s: &mut App) {
    s.ov.list.failed = None;
    soon(s, Duration::ZERO);
}

pub fn removed(s: &mut App, gone: &[Removal]) {
    let mut again = s.ov.list.asked.is_some();
    if let Some(listing) = s.ov.list.listing.as_mut() {
        for x in gone {
            again |= listing.remove(&x.id) || x.unlisted;
        }
    }
    if again {
        soon(s, SETTLE);
    }
}

fn refresh_at(listing: &Listing) -> Option<Duration> {
    let at = feed::catalog::parse_time(listing.view.refresh_at.as_deref()?)?;
    let wait = at - time::OffsetDateTime::now_utc();
    Some(Duration::try_from(wait).unwrap_or_default().max(SOONEST))
}

fn take(s: &mut App, read: Read, request: Request, got: Result<Got, String>) {
    let st = &mut s.ov.list;
    match got {
        Ok(Got::Page(page)) if matches!(read, Read::Fill(_)) => {
            if let Some(held) = st.listing.as_mut() {
                held.fill(page);
            }
        }
        Err(error) if matches!(read, Read::Fill(_)) => st.error = Some(error),
        Ok(got) => {
            let listing = match (got, st.listing.take()) {
                (Got::Page(page), Some(mut held)) if read == Read::More => {
                    held.append(page);
                    held
                }
                (Got::Page(page), _) => Listing::first(page),
                (Got::Whole(whole), _) => whole,
            };
            for x in &listing.view.surfaces {
                if !st.surfaces.contains(&x.surface) {
                    st.surfaces.push(x.surface.clone());
                }
            }
            st.surfaces.sort();
            for p in &listing.view.projects {
                if !p.id.is_empty() && !st.projects.iter().any(|(id, _)| *id == p.id) {
                    st.projects.push((p.id.clone(), p.name.clone()));
                }
            }
            if read == Read::First {
                st.expanded.clear();
            }
            if read != Read::More {
                st.filled.clear();
            }
            let wait = refresh_at(&listing);
            st.listing = Some(listing);
            st.shown = Some(request);
            if let Some(wait) = wait {
                soon(s, wait);
            }
        }
        Err(error) => {
            st.error = Some(error);
            st.failed = Some(request);
        }
    }
}

pub fn poll(s: &mut App) -> bool {
    let mut changed = false;
    while let Ok((seq, read, request, got)) = s.ov.list.rx.try_recv() {
        if s.ov.list.asked.as_ref().is_none_or(|a| a.0 != seq) {
            continue;
        }
        s.ov.list.asked = None;
        changed = true;
        if request != self::request(s) {
            continue;
        }
        take(s, read, request, got);
    }
    let visible = matches!(s.screen, Screen::Sessions);
    s.ov.list.visible = visible;
    if !visible || s.client.is_none() {
        return changed;
    }
    let want = request(s);
    let asked = s.ov.list.asked.as_ref().map(|a| &a.2);
    let current = asked.or(s.ov.list.failed.as_ref()).or(s.ov.list.shown.as_ref());
    if current != Some(&want) {
        let typing = current.is_some_and(|c| Request { query: want.query.clone(), ..c.clone() } == want);
        if typing && s.ov.list.typed.as_ref().is_none_or(|t| t.0 != want) {
            s.ov.list.typed = Some((want, Instant::now() + TYPING));
            crate::wake::due(Instant::now() + TYPING);
            return changed;
        }
        if typing && s.ov.list.typed.as_ref().is_some_and(|t| !crate::wake::due(t.1)) {
            return changed;
        }
        ask(s, Read::First, want);
        return true;
    }
    s.ov.list.typed = None;
    if s.ov.list.asked.is_some() {
        return changed;
    }
    if s.ov.list.stale {
        s.ov.list.due = Some(Instant::now());
    }
    if s.ov.list.due.is_some_and(crate::wake::due) {
        ask(s, Read::Again, want);
        return true;
    }
    let st = &s.ov.list;
    let short = st.listing.as_ref().map(Listing::short).unwrap_or_default();
    let paged = st.listing.as_ref().is_some_and(|l| l.next.is_some());
    if let Some(project) = short.into_iter().find(|p| st.expanded.contains(p) && !st.filled.contains(p) && (!p.is_empty() || paged)) {
        if project.is_empty() {
            ask(s, Read::More, want);
        } else {
            s.ov.list.filled.insert(project.clone());
            ask(s, Read::Fill(project), want);
        }
        return true;
    }
    changed
}
