use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::time::Duration;

use feed::catalog::Client;
use feed::look::{self as fl, Fit, Look, Rgb, Tint, View, Wallpaper};
use rune::{Color, Cx, El, Pixels, PixelsWrap, SystemMaterial, Tx, el};
use rune_kit::Kit;

use crate::theme::{self, Paint};

const IMAGE_SIDE: u32 = 2560;
const SAMPLE_SIDE: u32 = 512;
const RETRY: Duration = Duration::from_secs(5);

type Raw = (u32, u32, Vec<u8>);

struct Picture {
    pixels: Pixels,
    original: (u32, u32),
    brightest: Raw,
    darkest: Raw,
    worst: HashMap<(bool, usize), Rgb>,
}

enum Got {
    Device(String),
    View(u64, Box<View>),
    Image(String, Result<fl::Decoded, String>),
    Problem(Option<String>),
}

#[derive(Default)]
struct State {
    look: Look,
    view: Option<View>,
    device: String,
    pictures: HashMap<String, Result<Picture, String>>,
    meshes: HashMap<String, Picture>,
    kits: HashMap<String, &'static Kit>,
    rx: Option<Receiver<Got>>,
    tx: Option<Sender<Got>>,
    problem: Option<String>,
    area: (f32, f32),
    scale: f32,
    applied: u64,
    generation: u64,
    seen_main: u64,
    seen_settings: u64,
    increased: bool,
}

thread_local! {
    static STATE: RefCell<State> = RefCell::new(State { area: (1440.0, 900.0), scale: 2.0, ..State::default() });
}

pub fn view() -> Option<View> {
    STATE.with_borrow(|s| s.view.clone())
}

pub fn device() -> String {
    STATE.with_borrow(|s| s.device.clone())
}

pub fn problem() -> Option<String> {
    STATE.with_borrow(|s| {
        let failed = s.look.images().into_iter().find_map(|p| match s.pictures.get(&p) {
            Some(Err(e)) => Some(format!("Couldn't show the background {p}: {e}")),
            _ => None,
        });
        s.problem.clone().or(failed)
    })
}

fn scheme_of(look: &Look) -> rune::ColorScheme {
    match look.theme {
        fl::Theme::System => rune::ColorScheme::System,
        fl::Theme::Dark => rune::ColorScheme::Dark,
        fl::Theme::Light => rune::ColorScheme::Light,
    }
}

static TICKET: AtomicU64 = AtomicU64::new(0);

pub fn ticket() -> u64 {
    TICKET.fetch_add(1, Ordering::Relaxed) + 1
}

pub fn take_view(ticket: u64, view: View) {
    STATE.with_borrow_mut(|s| {
        if let Some(tx) = &s.tx {
            let _ = tx.send(Got::View(ticket, Box::new(view)));
        }
    });
}

fn start(s: &mut State) {
    let (tx, rx) = channel();
    s.rx = Some(rx);
    s.tx = Some(tx.clone());
    std::thread::spawn(move || follow(tx));
}

fn follow(tx: Sender<Got>) {
    let client = loop {
        match Client::from_env() {
            Ok(client) => break client,
            Err(e) => {
                if tx.send(Got::Problem(Some(format!("Can't read appearance settings without Agenthail's sign-in ({e}); retrying.")))).is_err() {
                    return;
                }
                std::thread::sleep(RETRY);
            }
        }
    };
    let device = fl::device_name();
    if tx.send(Got::Device(device.clone())).is_err() {
        return;
    }
    let class = fl::device_class();
    let mut fetched: Vec<String> = Vec::new();
    loop {
        let cursor = client.base_settings().map(|b| b.event_cursor).unwrap_or(0);
        match read(&client, class, &device, &tx, &mut fetched) {
            Read::Closed => return,
            Read::Retry => {
                std::thread::sleep(RETRY);
                continue;
            }
            Read::Done => {}
        }
        let mut after = Read::Done;
        let result = client.follow_events(cursor, || {}, |kind, _| {
            if kind != "ui.changed" && kind != "stream.reset" {
                return true;
            }
            after = read(&client, class, &device, &tx, &mut fetched);
            after == Read::Done
        });
        match after {
            Read::Closed => return,
            Read::Retry => std::thread::sleep(RETRY),
            Read::Done => {
                if let Err(e) = result
                    && tx.send(Got::Problem(Some(format!("Can't follow appearance changes right now ({e}); retrying.")))).is_err()
                {
                    return;
                }
                std::thread::sleep(Duration::from_secs(2));
            }
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Read {
    Done,
    Retry,
    Closed,
}

fn read(client: &Client, class: &str, device: &str, tx: &Sender<Got>, fetched: &mut Vec<String>) -> Read {
    let ticket = ticket();
    let view = match client.ui_view(class, device) {
        Ok(view) => view,
        Err(f) => {
            let sent = tx.send(Got::Problem(Some(format!("Couldn't read appearance settings: {}; retrying.", f.message))));
            return if sent.is_ok() { Read::Retry } else { Read::Closed };
        }
    };
    let paths = Look::from_view(&view).images();
    fetched.retain(|p| paths.contains(p));
    if tx.send(Got::View(ticket, Box::new(view))).is_err() {
        return Read::Closed;
    }
    let mut outcome = Read::Done;
    for path in paths {
        if fetched.contains(&path) {
            continue;
        }
        let got = client.ui_background(&path).and_then(|data| fl::decode(&data, IMAGE_SIDE, SAMPLE_SIDE));
        if got.is_ok() {
            fetched.push(path.clone());
        } else {
            outcome = Read::Retry;
        }
        if tx.send(Got::Image(path, got)).is_err() {
            return Read::Closed;
        }
    }
    outcome
}

fn picture(d: fl::Decoded) -> Picture {
    let pixels = Pixels::new();
    let (w, h, rgba) = d.shown;
    pixels.set(w, h, rgba);
    Picture { pixels, original: d.original, brightest: d.brightest, darkest: d.darkest, worst: HashMap::new() }
}

impl Picture {
    fn size(&self, scale: f32) -> (f32, f32) {
        (self.original.0 as f32 / scale.max(1.0), self.original.1 as f32 / scale.max(1.0))
    }

    fn worst(&mut self, dark: bool, blur: f32, area: (f32, f32)) -> Rgb {
        let sample = if dark { &self.brightest } else { &self.darkest };
        let per_point = sample.0.max(sample.1) as f32 / area.0.max(area.1).max(1.0);
        let cell = (blur * per_point).round().max(1.0) as usize;
        *self.worst.entry((dark, cell)).or_insert_with(|| fl::worst(&sample.2, sample.0, dark, cell))
    }
}

fn relook(s: &mut State) {
    if let Some(v) = &s.view {
        s.look = if s.increased { Look::with_increased_contrast(v) } else { Look::from_view(v) };
    }
}

fn pump(s: &mut State) {
    if s.rx.is_none() {
        start(s);
    }
    let got: Vec<Got> = s.rx.as_ref().map(|rx| rx.try_iter().collect()).unwrap_or_default();
    for g in got {
        s.generation += 1;
        match g {
            Got::Device(d) => s.device = d,
            Got::View(ticket, v) if ticket > s.applied => {
                s.applied = ticket;
                s.view = Some(*v);
                relook(s);
                let keep = s.look.images();
                s.pictures.retain(|p, _| keep.contains(p));
                let meshes = [wallpaper_key(&s.look.background), wallpaper_key(&s.look.background_light)];
                s.meshes.retain(|k, _| meshes.contains(k));
                s.problem = None;
            }
            Got::View(..) => {}
            Got::Image(path, Ok(d)) => {
                let p = picture(d);
                s.pictures.insert(path, Ok(p));
            }
            Got::Image(path, Err(e)) => {
                s.pictures.insert(path, Err(e));
            }
            Got::Problem(p) => s.problem = p,
        }
    }
}

pub enum Window {
    Main,
    Settings,
}

pub fn poll(window: Window, tx: &mut Tx) -> bool {
    let (changed, other) = STATE.with_borrow_mut(|s| {
        pump(s);
        let generation = s.generation;
        let (mine, theirs) = match window {
            Window::Main => (&mut s.seen_main, s.seen_settings),
            Window::Settings => (&mut s.seen_settings, s.seen_main),
        };
        let changed = *mine != generation;
        *mine = generation;
        if changed && s.view.is_some() {
            tx.set_color_scheme(scheme_of(&s.look));
        }
        (changed, theirs != generation)
    });
    if other && matches!(window, Window::Main) {
        crate::settings::nudge();
    }
    changed
}

fn rgb(c: Rgb) -> Color {
    Color { r: fl::linear(c[0]), g: fl::linear(c[1]), b: fl::linear(c[2]), a: 1.0 }
}

fn srgb(c: Color) -> Rgb {
    [fl::gamma(c.r), fl::gamma(c.g), fl::gamma(c.b)]
}

fn ground_rgb(dark: bool) -> Rgb {
    if dark { [0.086, 0.086, 0.094] } else { [0.957, 0.957, 0.965] }
}

fn wallpaper_key(w: &Wallpaper) -> String {
    format!("{w:?}")
}

fn worst_of(s: &mut State, dark: bool) -> Option<Rgb> {
    let w = s.look.wallpaper(dark).clone();
    let blur = if s.look.translucent(dark) { s.look.blur / 3.0 } else { 0.0 };
    let area = s.area;
    match &w {
        Wallpaper::Image(path) => s.pictures.get_mut(path).and_then(|p| p.as_mut().ok()).map(|p| p.worst(dark, blur, area)),
        Wallpaper::Mesh { .. } => mesh_picture(s, &w).map(|p| p.worst(dark, blur, area)),
        Wallpaper::Linear { stops, .. } => {
            let key = |c: &Rgb| fl::luminance(*c);
            let colors = stops.iter().map(|(c, _)| *c);
            if dark { colors.max_by(|a, b| key(a).total_cmp(&key(b))) } else { colors.min_by(|a, b| key(a).total_cmp(&key(b))) }
        }
        Wallpaper::None | Wallpaper::Desktop => None,
    }
}

fn mesh_picture<'a>(s: &'a mut State, w: &Wallpaper) -> Option<&'a mut Picture> {
    let Wallpaper::Mesh { columns, rows, colors } = w else { return None };
    let key = wallpaper_key(w);
    if !s.meshes.contains_key(&key) {
        let (mw, mh, rgba) = fl::mesh(*columns, *rows, colors, 1.0);
        let d = fl::Decoded { original: (mw, mh), shown: (mw, mh, rgba.clone()), brightest: (mw, mh, rgba.clone()), darkest: (mw, mh, rgba) };
        s.meshes.insert(key.clone(), picture(d));
    }
    s.meshes.get_mut(&key)
}

fn tint_rgb(look: &Look, dark: bool, accent: Rgb) -> Rgb {
    match look.tint {
        Tint::Ground => ground_rgb(dark),
        Tint::Accent => accent,
        Tint::Color(c) => c,
    }
}

fn quant(x: f32) -> u32 {
    (x.clamp(0.0, 1.0) * 255.0).round() as u32
}

fn cached(s: &mut State, key: String, build: impl FnOnce() -> Kit) -> &'static Kit {
    *s.kits.entry(key).or_insert_with(|| Box::leak(Box::new(build())))
}

fn accented(k: &mut Kit, a: Color) {
    k.c.bg_accent = a;
    k.c.text_accent = a;
    k.c.icon_accent = a;
    k.c.border_focus = a;
}

pub fn frame<S>(cx: &mut Cx<S>, session: bool, glass: bool) {
    let system_dark = cx.system_dark();
    let system_accent = srgb(cx.accent());
    let increased = cx.system_increased_contrast();
    let (paint, kit, chrome, scale) = STATE.with_borrow_mut(|s| {
        if increased != s.increased {
            s.increased = increased;
            relook(s);
        }
        let dark = s.look.dark(system_dark);
        let mut paint = if dark { Paint::dark() } else { Paint::light() };
        let accent_rgb = s.look.accent.map(|(d, l)| if dark { d } else { l }).unwrap_or(system_accent);
        let role = |name: &str, fallback: Color| {
            if s.look.accent_roles.iter().any(|r| r == name) {
                return rgb(accent_rgb);
            }
            s.look.role(name, dark).map(rgb).unwrap_or(fallback)
        };
        paint.text = role("body", paint.text);
        paint.muted = role("muted", paint.muted);
        paint.faint = role("faint", paint.faint);
        paint.attention = role("needs-you", paint.attention);
        paint.done = role("done", paint.done);
        paint.failed = role("error", paint.failed);
        paint.accent = rgb(accent_rgb);
        paint.accent_deep = rgb(fl::blend(accent_rgb, [0.0; 3], 0.35));
        let translucent = glass && s.look.translucent(dark);
        let drawn = s.look.drawn(dark);
        let mut key = format!("{dark}:{translucent}:{drawn}:{:?}:{:?}:{:?}", accent_rgb.map(quant), srgb(paint.text).map(quant), srgb(paint.muted).map(quant));
        if translucent {
            let ground = ground_rgb(dark);
            let tint = tint_rgb(&s.look, dark, accent_rgb);
            let strength = if session { s.look.session_opacity } else { s.look.opacity };
            let worst = worst_of(s, dark).unwrap_or(ground);
            let (pane, pane_rgb) = s.look.pane_tint(dark, worst, ground, tint, strength, s.look.pane_opacity);
            let (float, float_rgb) = s.look.pane_tint(dark, worst, ground, tint, strength, s.look.float_opacity);
            paint.card = paint.card.alpha(0.6);
            paint.card_deep = paint.card_deep.alpha(0.7);
            paint.raised = paint.raised.alpha(0.78);
            paint.glass_tint = rgb(float_rgb).alpha(float);
            paint.blur = s.look.blur / 3.0;
            paint.ground = rgb(ground);
            key = format!("{key}:{}:{}:{:?}", quant(pane), quant(s.look.border), pane_rgb.map(quant));
            paint.pane = rgb(pane_rgb).alpha(pane);
            paint.border = Color::WHITE.alpha(s.look.border);
        } else {
            paint.glass_tint = paint.card.alpha(if dark { 0.78 } else { 1.0 });
        }
        let kit = cached(s, format!("kit:{key}"), || {
            let mut k = Kit::neutral(dark);
            if translucent {
                k.c.bg_deep = paint.pane;
                k.c.bg_base = Color::WHITE.alpha(0.025);
                k.c.border_muted = paint.border;
            }
            accented(&mut k, paint.accent);
            k
        });
        let chrome = cached(s, format!("chrome:{key}"), || {
            let mut k = rune_theme_opencode::kit(dark).clone();
            if translucent {
                k.c.bg_deep = paint.pane;
                k.c.bg_base = Color::WHITE.alpha(0.025);
                k.c.border_muted = paint.border;
            } else if dark {
                k.c.bg_deep = if drawn { paint.card_deep } else { Color::TRANSPARENT };
                k.c.border_muted = Color::WHITE.alpha(0.09);
            }
            k.c.text_strong = paint.text;
            k.c.text_base = paint.text;
            k.c.text_weak = paint.muted;
            accented(&mut k, paint.accent);
            k
        });
        (paint, kit, chrome, s.look.text_scale)
    });
    theme::set(paint, kit, chrome);
    cx.zoom(scale);
}

pub fn backdrop(session: bool) -> El {
    STATE.with_borrow_mut(|s| {
        let paint = theme::paint();
        let dark = paint.dark;
        let base = el().key("backdrop").absolute(0.0, 0.0).w_full().h_full().on_layout_any(|r, tx| {
            let scale = tx.scale();
            STATE.with_borrow_mut(|s| {
                s.area = (r[2], r[3]);
                s.scale = scale;
            });
        });
        if !s.look.drawn(dark) {
            let base = base.fill(paint.card_deep);
            return if dark && s.look.glass { base.system_material(SystemMaterial::Glass) } else { base };
        }
        let strength = if session { s.look.session_opacity } else { s.look.opacity };
        let w = s.look.wallpaper(dark).clone();
        let art = match &w {
            Wallpaper::Image(path) => match s.pictures.get(path) {
                Some(Ok(p)) => {
                    let placed = fl::place(s.look.fit, s.look.position, p.size(s.scale), s.area);
                    let wrap = if s.look.fit == Fit::Tile { PixelsWrap::Repeat } else { PixelsWrap::Extend };
                    let full = el().w_full().h_full();
                    Some(if placed.tile || s.look.fit == Fit::Cover { full.pixels_wrap(&p.pixels, placed.rect, wrap) } else { el().clip_rounded(0.0).w_full().h_full().child(el().absolute(placed.rect[0], placed.rect[1]).size(placed.rect[2], placed.rect[3]).pixels(&p.pixels)) })
                }
                _ => None,
            },
            Wallpaper::Mesh { .. } => mesh_picture(s, &w).map(|p| el().w_full().h_full().pixels(&p.pixels)),
            Wallpaper::Linear { angle, stops } => {
                let stops: Vec<(f32, Color)> = stops.iter().map(|(c, at)| (*at, rgb(*c))).collect();
                Some(el().w_full().h_full().linear_gradient(angle.to_radians(), stops))
            }
            Wallpaper::None | Wallpaper::Desktop => None,
        };
        let blur = if s.look.translucent(dark) { s.look.blur / 3.0 } else { 0.0 };
        let mut root = base.clip_rounded(0.0).fill(paint.ground);
        if let Some(art) = art {
            root = root.child(el().absolute(0.0, 0.0).w_full().h_full().opacity(strength).blur(blur).child(art));
        }
        if s.look.dim > 0.0 {
            root = root.child(el().absolute(0.0, 0.0).w_full().h_full().fill(if dark { Color::BLACK } else { Color::WHITE }.alpha(s.look.dim)));
        }
        root
    })
}
