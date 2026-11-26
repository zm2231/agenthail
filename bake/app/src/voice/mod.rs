#[cfg(target_os = "macos")]
mod peer;
mod scenes;
mod view;

use std::cell::RefCell;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::Client;
#[cfg(target_os = "macos")]
use feed::voice::Peer;
use feed::voice::{Action, Call, Effect, Failure, Job, Req, State, Trail};
#[cfg(target_os = "macos")]
use feed::voice::Setup;
use rune::{AppSpec, Chrome, MicPermission, NativeView, TextEdit, Tx, Voice, WindowHandle, WindowOptions, WindowSpec};

thread_local! {
    static WINDOW: RefCell<Option<WindowHandle>> = const { RefCell::new(None) };
    static ROUTE: RefCell<Option<(String, String)>> = const { RefCell::new(None) };
    static OPENED: RefCell<Option<String>> = const { RefCell::new(None) };
    static UNSAVED: RefCell<Option<Trail>> = const { RefCell::new(None) };
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Ask {
    NewConversation,
    StopTarget,
}

pub type Heard = (String, String, String);

const SAVE_RETRY_SECS: f64 = 5.0;

pub struct Win {
    pub call: Call,
    pub name: String,
    pub offer: Option<(String, String)>,
    pub typed: TextEdit,
    pub route_edit: TextEdit,
    route_typed: String,
    pub details: bool,
    pub ask: Option<Ask>,
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
    keep_log: bool,
    #[cfg(target_os = "macos")]
    loads: u64,
}

impl Win {
    fn new(target: &str, name: &str) -> Win {
        let client = Client::from_env().ok();
        let peer = match &client {
            Some(c) => c.voice_peer(),
            None => Err("Voice needs the Agenthail daemon. Start it, then open this window again.".into()),
        };
        let mut call = Call::new(target);
        let unsaved = UNSAVED.with(|u| u.borrow_mut().take());
        let loaded = unsaved.or_else(load_trail);
        let keep_log = loaded.is_some();
        call.trail = loaded.unwrap_or_default();
        if let Err(error) = &peer {
            eprintln!("voice: {error}");
            call.error = Some(error.clone());
        }
        Win {
            call,
            name: name.to_string(),
            offer: None,
            typed: TextEdit::new(""),
            route_edit: TextEdit::new(target.trim()),
            route_typed: target.trim().to_string(),
            details: false,
            ask: None,
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
            keep_log,
            #[cfg(target_os = "macos")]
            loads: 0,
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
                Effect::PeerEnd => self.fresh_page(),
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
    fn fresh_page(&mut self) {
        let (Some(web), Ok(peer)) = (&self.web, &self.peer) else { return };
        self.loads += 1;
        web.eval("window.agenthailCall = \"\"; window.agenthailVoice && window.agenthailVoice.end()");
        web.reload(peer);
        let mut effects = self.call.peer("loading", "", self.clock);
        effects.extend(self.call.setup(Setup::Page));
        self.run(effects);
    }

    #[cfg(not(target_os = "macos"))]
    fn fresh_page(&mut self) {}

    #[cfg(target_os = "macos")]
    fn build_peer(&mut self) -> bool {
        if self.web.is_some() || self.call.closed() {
            return false;
        }
        let Ok(peer) = self.peer.clone() else { return false };
        if !self.slot.in_window() {
            let effects = self.call.setup(Setup::Window);
            let said = !effects.is_empty();
            self.run(effects);
            return said;
        }
        let effects = self.call.peer("loading", "", self.clock);
        self.run(effects);
        let effects = self.call.setup(Setup::Page);
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
    if w.call.trail.changed && w.keep_log && !save_trail(&w.call.trail) {
        let trail = std::mem::take(&mut w.call.trail);
        UNSAVED.with(|u| *u.borrow_mut() = Some(trail));
    }
    #[cfg(target_os = "macos")]
    {
        w.web = None;
    }
    WINDOW.with(|slot| *slot.borrow_mut() = None);
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
    let mut changed = w.build_peer();
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
        if kind == peer::LOADED {
            w.loads += 1;
            let effects = w.call.setup(Setup::Check);
            changed |= !effects.is_empty();
            w.run(effects);
            if let Some(web) = &w.web {
                web.check(w.heard.0.clone(), w.loads);
            }
            continue;
        }
        #[cfg(target_os = "macos")]
        if kind == peer::CHECKED {
            if call == w.loads.to_string() {
                let effects = w.call.checked(value == "true", t);
                changed |= !effects.is_empty();
                w.run(effects);
            }
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
    let effects = w.call.tick(t);
    changed |= effects.iter().any(|e| matches!(e, Effect::Note(_)));
    w.run(effects);
    if w.call.trail.changed && w.keep_log && t >= w.save_after {
        if save_trail(&w.call.trail) {
            w.call.trail.changed = false;
        } else {
            w.save_after = t + SAVE_RETRY_SECS;
        }
    }
    tx.wake_after(Duration::from_millis(100));
    trace("poll", w);
    changed
}


fn trail_file() -> Option<std::path::PathBuf> {
    if rune::is_headless() {
        return None;
    }
    let home = std::env::var_os("HOME")?;
    Some(std::path::PathBuf::from(home).join("Library/Application Support/Agenthail/voice-calls.json"))
}

fn load_trail() -> Option<Trail> {
    let Some(path) = trail_file() else { return Some(Trail::default()) };
    let saved = match std::fs::read_to_string(&path) {
        Ok(saved) => saved,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Some(Trail::default()),
        Err(error) => {
            eprintln!("voice: the call log couldn't be read, so this window won't write it: {error}");
            return None;
        }
    };
    match Trail::load(&saved) {
        Ok(trail) => Some(trail),
        Err(error) => {
            let aside = unused_aside(&path);
            match std::fs::rename(&path, &aside) {
                Ok(()) => {
                    eprintln!("voice: the call log couldn't be read ({error}); it was moved to {}", aside.display());
                    Some(Trail::default())
                }
                Err(moved) => {
                    eprintln!("voice: the call log couldn't be read ({error}) or moved aside ({moved}), so this window won't write it");
                    None
                }
            }
        }
    }
}

fn unused_aside(path: &std::path::Path) -> std::path::PathBuf {
    let stamp = time::OffsetDateTime::now_utc().unix_timestamp();
    (0..)
        .map(|n| path.with_extension(if n == 0 { format!("unreadable-{stamp}.json") } else { format!("unreadable-{stamp}-{n}.json") }))
        .find(|aside| !aside.exists())
        .unwrap_or_else(|| path.with_extension("unreadable.json"))
}

fn save_trail(trail: &Trail) -> bool {
    let Some(path) = trail_file() else { return true };
    let part = path.with_extension("json.part");
    let written = path.parent().map_or(Ok(()), std::fs::create_dir_all).and_then(|()| std::fs::write(&part, trail.save())).and_then(|()| std::fs::rename(&part, &path));
    if let Err(error) = &written {
        eprintln!("voice: the call log couldn't be saved: {error}");
    }
    written.is_ok()
}
