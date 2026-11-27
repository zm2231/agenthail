#[cfg(target_os = "macos")]
mod peer;
mod scenes;
mod view;

use std::cell::RefCell;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::Client;
#[cfg(target_os = "macos")]
use feed::voice::Peer;
use feed::derive::World;
use feed::voice::{Action, Activity, Call, Effect, Failure, Job, Pick, Req, State, Trail};
#[cfg(target_os = "macos")]
use feed::voice::Setup;
use rune::{AppSpec, Chrome, MicPermission, NativeView, TextEdit, Tx, Voice, WindowHandle, WindowOptions, WindowSpec};

thread_local! {
    static WINDOW: RefCell<Option<WindowHandle>> = const { RefCell::new(None) };
    static ROUTE: RefCell<Option<(String, String)>> = const { RefCell::new(None) };
    static OPENED: RefCell<Option<String>> = const { RefCell::new(None) };
    static UNSAVED: RefCell<Option<Trail>> = const { RefCell::new(None) };
    static SHARED: RefCell<Shared> = RefCell::new(Shared::default());
}

#[derive(Default)]
struct Shared {
    version: u64,
    built: Option<f64>,
    operator: String,
    shared_for: String,
    picks: Vec<Pick>,
    activity: Option<Activity>,
}

pub fn share(built: f64, world: &World) {
    if WINDOW.with(|w| w.borrow().is_none()) {
        return;
    }
    SHARED.with_borrow_mut(|s| {
        if s.built == Some(built) && s.shared_for == s.operator {
            return;
        }
        s.built = Some(built);
        s.shared_for = s.operator.clone();
        s.picks = world.voice_picks();
        s.activity = world.voice_activity(&s.operator);
        s.version += 1;
    });
}

pub fn operator() -> Option<String> {
    WINDOW.with(|w| w.borrow().is_some()).then(|| SHARED.with_borrow(|s| s.operator.clone())).filter(|id| !id.is_empty())
}

fn reset_shared() {
    SHARED.with_borrow_mut(|s| *s = Shared { version: s.version + 1, ..Shared::default() });
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Ask {
    NewConversation,
    StopTarget,
    Interrupt,
}

pub type Heard = (String, String, String);

const SAVE_RETRY_SECS: f64 = 5.0;

pub struct Win {
    pub call: Call,
    pub name: String,
    pub offer: Option<(String, String)>,
    pub typed: TextEdit,
    pub typed_id: Option<String>,
    pub route_edit: TextEdit,
    route_typed: String,
    pub details: bool,
    pub ask: Option<Ask>,
    pub picks: Vec<Pick>,
    pub activity: Option<Activity>,
    pub picking: Option<usize>,
    shared_seen: u64,
    pub slot: NativeView,
    client: Option<Client>,
    #[cfg(target_os = "macos")]
    peer: Result<Peer, String>,
    #[cfg(target_os = "macos")]
    web: Option<peer::Web>,
    replies: (Sender<(Job, Result<State, Failure>)>, Receiver<(Job, Result<State, Failure>)>),
    heard: (Sender<Heard>, Receiver<Heard>),
    mic: (Sender<(String, bool)>, Receiver<(String, bool)>),
    gone: Arc<AtomicBool>,
    clock: f64,
    save_after: f64,
    #[cfg(target_os = "macos")]
    page_spent: bool,
}

impl Win {
    fn new(target: &str, name: &str) -> Win {
        let client = Client::from_env().ok();
        let peer = match &client {
            Some(c) => c.voice_peer(),
            None => Err("Voice needs the Agenthail daemon. Start it, then open this window again.".into()),
        };
        let mut call = Call::new(target);
        call.trail = UNSAVED.with(|u| u.borrow_mut().take()).unwrap_or_else(load_trail);
        if let Err(error) = &peer {
            eprintln!("voice: {error}");
            call.error = Some(error.clone());
        }
        Win {
            call,
            name: name.to_string(),
            offer: None,
            typed: TextEdit::new(""),
            typed_id: None,
            route_edit: TextEdit::new(target.trim()),
            route_typed: target.trim().to_string(),
            details: false,
            ask: None,
            picks: Vec::new(),
            activity: None,
            picking: None,
            shared_seen: 0,
            slot: NativeView::new(),
            client,
            #[cfg(target_os = "macos")]
            peer,
            #[cfg(target_os = "macos")]
            web: None,
            replies: channel(),
            heard: channel(),
            mic: channel(),
            gone: Arc::new(AtomicBool::new(false)),
            clock: 0.0,
            save_after: 0.0,
            #[cfg(target_os = "macos")]
            page_spent: false,
        }
    }

    fn blank() -> Win {
        Win::new("", "")
    }

    pub fn run(&mut self, effects: Vec<Effect>) {
        for effect in effects {
            match effect {
                Effect::Fetch(job) => self.fetch(job),
                Effect::AskMic(attempt) => ask_mic(self.mic.0.clone(), attempt),
                Effect::PeerStart(attempt) => {
                    let quoted = serde_json::to_string(&attempt).unwrap_or_default();
                    self.eval(&format!("window.agenthailCall = {quoted}; void window.agenthailVoice.start()"));
                }
                Effect::PeerEnd => self.end_page(),
                Effect::PeerAnswer(sdp) => {
                    let quoted = serde_json::to_string(&sdp).unwrap_or_default();
                    self.eval(&format!("void window.agenthailVoice.answer({quoted})"));
                }
                Effect::PeerMute(muted) => self.eval(&format!("window.agenthailVoice && window.agenthailVoice.mute({muted})")),
                Effect::Note(note) => eprintln!("voice: {note}"),
            }
        }
    }

    fn fetch(&self, job: Job) {
        let Some(client) = self.client.clone() else { return };
        let (tx, gone) = (self.replies.0.clone(), self.gone.clone());
        std::thread::spawn(move || {
            let result = match &job.action {
                Some(action) => client.voice(action),
                None => client.voice_state().map_err(Failure::from),
            };
            if let Req::Start(attempt) = &job.req {
                if gone.load(Ordering::SeqCst) {
                    let _ = client.voice(&Action { attempt_id: Some(attempt.clone()), ..Action::new("stop") });
                    return;
                }
            }
            let _ = tx.send((job, result));
        });
    }

    #[cfg(target_os = "macos")]
    fn eval(&self, js: &str) {
        if let Some(web) = &self.web {
            web.eval(js);
        }
    }

    #[cfg(not(target_os = "macos"))]
    fn eval(&self, _: &str) {}

    #[cfg(target_os = "macos")]
    fn end_page(&mut self) {
        let Some(web) = &self.web else { return };
        web.eval("window.agenthailCall = \"\"; window.agenthailVoice && window.agenthailVoice.end()");
        self.page_spent = true;
    }

    #[cfg(not(target_os = "macos"))]
    fn end_page(&mut self) {}

    #[cfg(target_os = "macos")]
    fn fresh_page(&mut self) -> bool {
        if !std::mem::take(&mut self.page_spent) {
            return false;
        }
        let (Some(web), Ok(peer)) = (&self.web, &self.peer) else { return false };
        web.reload(peer);
        let mut effects = self.call.peer("loading", "", self.clock);
        effects.extend(self.call.setup(Setup::Page, self.clock));
        self.run(effects);
        true
    }

    #[cfg(not(target_os = "macos"))]
    fn fresh_page(&mut self) -> bool {
        false
    }

    #[cfg(target_os = "macos")]
    fn build_peer(&mut self) -> bool {
        if self.web.is_some() || self.call.closed() {
            return false;
        }
        let Ok(peer) = self.peer.clone() else { return false };
        if !self.slot.in_window() {
            let effects = self.call.setup(Setup::Window, self.clock);
            let said = !effects.is_empty();
            self.run(effects);
            return said;
        }
        let effects = self.call.peer("loading", "", self.clock);
        self.run(effects);
        let effects = self.call.setup(Setup::Page, self.clock);
        self.run(effects);
        match peer::Web::build(&self.slot, &peer, self.heard.0.clone()) {
            Ok(web) => self.web = Some(web),
            Err(error) => {
                let error = format!("The audio page couldn't open: {error}");
                self.peer = Err(error.clone());
                let effects = self.call.peer("unavailable", &error, self.clock);
                self.run(effects);
            }
        }
        true
    }

    #[cfg(not(target_os = "macos"))]
    fn build_peer(&mut self) -> bool {
        false
    }

    fn take_shared(&mut self) -> bool {
        let operator = self.call.state.as_ref().and_then(|s| s.session.as_ref()).map(|p| p.id.clone()).unwrap_or_default();
        SHARED.with_borrow_mut(|s| {
            let moved = s.operator != operator;
            s.operator = operator;
            let cleared = moved && self.activity.take().is_some();
            if s.version == self.shared_seen {
                return cleared;
            }
            self.shared_seen = s.version;
            self.picks = s.picks.clone();
            self.activity = s.activity.clone().filter(|_| s.shared_for == s.operator);
            true
        })
    }

    pub fn pick(&mut self, pick: &Pick) {
        self.picking = None;
        self.route(&pick.id, &pick.label());
    }

    pub fn sync_route(&mut self) -> bool {
        let typed = self.route_edit.text.trim().to_string();
        if typed == self.route_typed {
            return false;
        }
        self.route(&typed, "");
        true
    }

    pub fn route(&mut self, target: &str, name: &str) {
        self.route_typed = target.trim().to_string();
        if self.route_edit.text.trim() != self.route_typed {
            self.route_edit = TextEdit::new(self.route_typed.as_str());
        }
        if self.call.active() {
            self.offer = (target.trim() != self.call.target).then(|| (target.trim().to_string(), name.to_string()));
            return;
        }
        self.call.target = target.trim().to_string();
        self.name = name.to_string();
        self.offer = None;
    }
}

fn ask_mic(tx: Sender<(String, bool)>, attempt: String) {
    match Voice::permission() {
        MicPermission::Granted => {
            let _ = tx.send((attempt, true));
        }
        MicPermission::Undetermined => Voice::request_permission(move |answer| {
            let _ = tx.send((attempt, answer == MicPermission::Granted));
        }),
        _ => {
            let _ = tx.send((attempt, false));
        }
    }
}

pub fn open(target: &str, name: &str) {
    let open = WINDOW.with(|w| w.borrow().clone());
    if let Some(handle) = open {
        ROUTE.with(|r| *r.borrow_mut() = Some((target.to_string(), name.to_string())));
        handle.focus();
        return;
    }
    let window = WindowSpec::new("voice", spec())
        .state(Win::new(target, name))
        .chrome(CHROME)
        .options(WindowOptions { transparent: true, resizable: true, visible: true, ..Default::default() })
        .srgb_blend(true)
        .map_app(|app| app.on_close(closed));
    reset_shared();
    let handle = rune::open_window(window);
    WINDOW.with(|w| *w.borrow_mut() = Some(handle));
}

fn spec() -> AppSpec<Win> {
    AppSpec::<Win> {
        poll: Some(poll),
        fonts: rune_theme_opencode::FONTS,
        families: rune_theme_opencode::FAMILIES,
        min_size: Some((420.0, 480.0)),
        scenes: scenes::SCENES,
        ..AppSpec::new("Talk to orchestrator", (460.0, 620.0), Win::blank, view::view)
    }
}

pub fn run_alone() {
    rune::App::new(spec())
        .chrome(CHROME)
        .window_options(WindowOptions { transparent: true, resizable: true, visible: true, ..Default::default() })
        .srgb_blend(true)
        .on_close(closed)
        .run();
}

const CHROME: Chrome = Chrome { full_size_content: true, traffic_lights: Some((16.0, 18.0)), drag_regions: true };

fn closed(w: &mut Win, _: &mut Tx) {
    w.gone.store(true, Ordering::SeqCst);
    let effects = w.call.close();
    w.run(effects);
    if w.call.trail.changed && !save_trail(&mut w.call.trail) {
        let trail = std::mem::take(&mut w.call.trail);
        UNSAVED.with(|u| *u.borrow_mut() = Some(trail));
    }
    #[cfg(target_os = "macos")]
    {
        w.web = None;
    }
    WINDOW.with(|slot| *slot.borrow_mut() = None);
    reset_shared();
}

pub fn take_opened_session() -> Option<String> {
    OPENED.with(|o| o.borrow_mut().take())
}

pub fn open_timeline(id: &str) {
    OPENED.with(|o| *o.borrow_mut() = Some(id.to_string()));
}

thread_local! {
    static TRACED: RefCell<Vec<(&'static str, String)>> = const { RefCell::new(Vec::new()) };
}

pub fn trace(from: &'static str, w: &Win) {
    let seen = format!("state {:p}: {} / {}", w, w.call.title(), w.call.unavailable().unwrap_or_else(|| "can call".into()));
    let fresh = TRACED.with_borrow_mut(|t| match t.iter_mut().find(|(f, _)| *f == from) {
        Some((_, last)) if *last == seen => false,
        Some((_, last)) => {
            *last = seen.clone();
            true
        }
        None => {
            t.push((from, seen.clone()));
            true
        }
    });
    if fresh {
        eprintln!("voice: {from} sees {seen}");
    }
}

fn poll(w: &mut Win, t: f64, tx: &mut Tx) -> bool {
    w.clock = t;
    let mut changed = w.fresh_page();
    changed |= w.build_peer();
    if let Some((target, name)) = ROUTE.with(|r| r.borrow_mut().take()) {
        w.route(&target, &name);
        changed = true;
    }
    changed |= w.sync_route();
    if !w.call.active() {
        if let Some((target, name)) = w.offer.take() {
            w.route(&target, &name);
            changed = true;
        }
    }
    if !w.call.working() && w.offer.as_ref().is_some_and(|(target, _)| *target == w.call.target) {
        if let Some((_, name)) = w.offer.take() {
            w.name = name;
        }
        changed = true;
    }
    while let Ok((job, result)) = w.replies.1.try_recv() {
        let effects = w.call.reply(job, result, t);
        w.run(effects);
        changed = true;
    }
    while let Ok((call, kind, value)) = w.heard.1.try_recv() {
        match kind.as_str() {
            "loaded" => {}
            "offer" => eprintln!("voice: the audio page made its offer"),
            _ if value.is_empty() => eprintln!("voice: the audio page said {kind}"),
            _ => eprintln!("voice: the audio page said {kind}: {value}"),
        }
        #[cfg(target_os = "macos")]
        if [peer::LOADED, peer::MISLOADED, peer::CHECKED].contains(&kind.as_str()) {
            let Some(navigation) = w.web.as_ref().map(peer::Web::navigation) else { continue };
            if call != navigation {
                continue;
            }
            let effects = match kind.as_str() {
                peer::LOADED => {
                    if let Some(web) = &w.web {
                        web.check(w.heard.0.clone(), navigation);
                    }
                    w.call.setup(Setup::Check, t)
                }
                peer::MISLOADED => w.call.peer("unavailable", &value, t),
                _ => w.call.checked(value == "true", t),
            };
            w.run(effects);
            changed = true;
            continue;
        }
        let effects = w.call.heard(&call, &kind, &value, t);
        w.run(effects);
        changed = true;
    }
    while let Ok((attempt, granted)) = w.mic.1.try_recv() {
        let effects = w.call.mic(&attempt, granted);
        w.run(effects);
        changed = true;
    }
    changed |= w.take_shared();
    let sent = w.call.take_sent();
    if w.typed_id.as_ref().is_some_and(|id| sent.iter().any(|(s, _)| s == id)) {
        w.typed = TextEdit::new("");
        w.typed_id = None;
        changed = true;
    }
    let effects = w.call.tick(t);
    changed |= effects.iter().any(|e| matches!(e, Effect::Note(_)));
    w.run(effects);
    if w.call.trail.changed && t >= w.save_after {
        if save_trail(&mut w.call.trail) {
            w.call.trail.changed = false;
        } else {
            w.save_after = t + SAVE_RETRY_SECS;
        }
    }
    tx.wake_after(Duration::from_millis(100));
    trace("poll", w);
    changed
}


fn trail_file() -> Option<PathBuf> {
    if rune::is_headless() {
        return None;
    }
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join("Library/Application Support/Agenthail/voice-calls.json"))
}

fn load_trail() -> Trail {
    let pid = std::process::id();
    let saved = trail_file().map(std::fs::read_to_string);
    match saved {
        Some(Ok(saved)) => Trail::load(&saved, pid, running).unwrap_or_else(|_| Trail::owned_by(pid)),
        Some(Err(error)) if error.kind() != std::io::ErrorKind::NotFound => {
            eprintln!("voice: the call log couldn't be read: {error}");
            Trail::owned_by(pid)
        }
        _ => Trail::owned_by(pid),
    }
}

#[cfg(unix)]
fn running(pid: u32) -> bool {
    let Ok(pid) = i32::try_from(pid) else { return false };
    let answered = unsafe { libc::kill(pid, 0) };
    answered == 0 || std::io::Error::last_os_error().raw_os_error() == Some(libc::EPERM)
}

#[cfg(not(unix))]
fn running(_: u32) -> bool {
    true
}

fn unused_aside(path: &Path) -> PathBuf {
    let stamp = time::OffsetDateTime::now_utc().unix_timestamp();
    (0..)
        .map(|n| path.with_extension(if n == 0 { format!("unreadable-{stamp}.json") } else { format!("unreadable-{stamp}-{n}.json") }))
        .find(|aside| !aside.exists())
        .unwrap_or_else(|| path.with_extension("unreadable.json"))
}

fn saved_trail(path: &Path) -> Result<Trail, String> {
    match std::fs::read_to_string(path) {
        Ok(saved) => Trail::parse(&saved).or_else(|error| {
            let aside = unused_aside(path);
            std::fs::rename(path, &aside).map_err(|moved| format!("it couldn't be read ({error}) or moved aside ({moved})"))?;
            eprintln!("voice: the call log couldn't be read ({error}); it was moved to {}", aside.display());
            Ok(Trail::default())
        }),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(Trail::default()),
        Err(error) => Err(format!("it couldn't be read: {error}")),
    }
}

fn save_trail(trail: &mut Trail) -> bool {
    let Some(path) = trail_file() else { return true };
    let written = (|| -> Result<(), String> {
        let dir = path.parent().ok_or("it has no folder")?;
        std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
        let lock = std::fs::File::options().create(true).truncate(false).write(true).open(dir.join("voice-calls.lock")).map_err(|e| e.to_string())?;
        lock.lock().map_err(|e| e.to_string())?;
        trail.combine(saved_trail(&path)?, running);
        let part = path.with_extension(format!("{}.part", std::process::id()));
        std::fs::write(&part, trail.save()).and_then(|()| std::fs::rename(&part, &path)).map_err(|e| e.to_string())
    })();
    if let Err(error) = &written {
        eprintln!("voice: the call log couldn't be saved: {error}");
    }
    written.is_ok()
}
