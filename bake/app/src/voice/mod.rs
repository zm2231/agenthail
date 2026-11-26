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
use feed::voice::{Action, Call, Effect, Failure, Job, Req, State};
use rune::{AppSpec, Chrome, MicPermission, NativeView, TextEdit, Tx, Voice, WindowHandle, WindowOptions, WindowSpec};

thread_local! {
    static WINDOW: RefCell<Option<WindowHandle>> = const { RefCell::new(None) };
    static ROUTE: RefCell<Option<(String, String)>> = const { RefCell::new(None) };
    static OPENED: RefCell<Option<String>> = const { RefCell::new(None) };
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Ask {
    NewConversation,
    StopTarget,
}

pub struct Win {
    pub call: Call,
    pub name: String,
    pub offer: Option<(String, String)>,
    pub typed: TextEdit,
    pub ask: Option<Ask>,
    pub slot: NativeView,
    client: Option<Client>,
    #[cfg(target_os = "macos")]
    peer: Result<Peer, String>,
    #[cfg(target_os = "macos")]
    web: Option<peer::Web>,
    replies: (Sender<(Job, Result<State, Failure>)>, Receiver<(Job, Result<State, Failure>)>),
    heard: (Sender<(String, String)>, Receiver<(String, String)>),
    mic: (Sender<(String, bool)>, Receiver<(String, bool)>),
    gone: Arc<AtomicBool>,
    clock: f64,
}

impl Win {
    fn new(target: &str, name: &str) -> Win {
        let client = Client::from_env().ok();
        let peer = match &client {
            Some(c) => c.voice_peer(),
            None => Err("Voice needs the Agenthail daemon. Start it, then open this window again.".into()),
        };
        let mut call = Call::new(target);
        if let Err(error) = &peer {
            call.error = Some(error.clone());
        }
        Win {
            call,
            name: name.to_string(),
            offer: None,
            typed: TextEdit::new(""),
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
                Effect::PeerStart => self.eval("void window.agenthailVoice.start()"),
                Effect::PeerEnd => self.eval("window.agenthailVoice && window.agenthailVoice.end()"),
                Effect::PeerAnswer(sdp) => {
                    let quoted = serde_json::to_string(&sdp).unwrap_or_default();
                    self.eval(&format!("void window.agenthailVoice.answer({quoted})"));
                }
                Effect::PeerMute(muted) => self.eval(&format!("window.agenthailVoice && window.agenthailVoice.mute({muted})")),
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
    fn build_peer(&mut self) {
        if self.web.is_some() || !self.slot.in_window() {
            return;
        }
        let Ok(peer) = self.peer.clone() else { return };
        let effects = self.call.peer("loading", "", self.clock);
        self.run(effects);
        match peer::Web::build(&self.slot, &peer, self.heard.0.clone()) {
            Ok(web) => self.web = Some(web),
            Err(error) => {
                let effects = self.call.peer("unavailable", &format!("The audio page couldn't open: {error}"), self.clock);
                self.run(effects);
            }
        }
    }

    #[cfg(not(target_os = "macos"))]
    fn build_peer(&mut self) {}

    pub fn route(&mut self, target: &str, name: &str) {
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

fn poll(w: &mut Win, t: f64, tx: &mut Tx) -> bool {
    w.clock = t;
    let mut changed = false;
    w.build_peer();
    if let Some((target, name)) = ROUTE.with(|r| r.borrow_mut().take()) {
        w.route(&target, &name);
        changed = true;
    }
    while let Ok((job, result)) = w.replies.1.try_recv() {
        let effects = w.call.reply(job, result, t);
        w.run(effects);
        changed = true;
    }
    while let Ok((kind, value)) = w.heard.1.try_recv() {
        #[cfg(target_os = "macos")]
        if kind == peer::LOADED {
            if let Some(web) = &w.web {
                web.check(w.heard.0.clone());
            }
            continue;
        }
        let effects = w.call.peer(&kind, &value, t);
        w.run(effects);
        changed = true;
    }
    while let Ok((attempt, granted)) = w.mic.1.try_recv() {
        let effects = w.call.mic(&attempt, granted);
        w.run(effects);
        changed = true;
    }
    let effects = w.call.tick(t);
    w.run(effects);
    tx.wake_after(Duration::from_millis(100));
    changed
}
