use std::cell::{Cell, RefCell};
use std::collections::HashMap;
use std::sync::atomic::{AtomicU32, AtomicU64, Ordering};
use std::sync::mpsc::Receiver;
use std::time::Duration;

use feed::catalog::Client;
use feed::wake::Sender;
use feed::watch::Reconnect;
use feed::look::{self as fl, Fit, Look, Rgb, Tint, View, Wallpaper};
use rune::{Color, Cx, El, Pixels, PixelsWrap, SystemMaterial, Tx, el};
use rune_kit::{Kit, KitCx, KitEnv, KitTheme, TextRoles, TextScale};

use legible::{Backdrops, hold_kit, hold_paint, over};

use crate::theme::{self, Paint};
use crate::wake::channel;

const SAMPLE_SIDE: u32 = 512;
const WASH: Color = Color { r: 1.0, g: 1.0, b: 1.0, a: 0.025 };
const SIGN_IN_WAIT: Duration = Duration::from_secs(10);
const FIRST_READ_RETRY: Duration = Duration::from_secs(10);

type Raw = (u32, u32, Vec<u8>);

struct Picture {
    pixels: Pixels,
    original: (u32, u32),
    brightest: Raw,
    darkest: Raw,
    worst: HashMap<(bool, usize), Rgb>,
    see_through: bool,
}

enum Got {
    Device(String),
    View(u64, Box<View>),
    Image(String, u32, Result<fl::Decoded, String>),
    Problem(Option<String>),
}

#[derive(Default)]
struct State {
    look: Look,
    view: Option<View>,
    device: String,
    pictures: HashMap<String, Result<Picture, String>>,
    meshes: HashMap<String, Picture>,
    kits: Vec<(String, KitTheme)>,
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
    screen: u32,
    text: TextScale,
    held_scale: Option<f32>,
}

thread_local! {
    static STATE: RefCell<State> = RefCell::new(State { area: (1440.0, 900.0), scale: 2.0, ..State::default() });
}

pub fn view() -> Option<View> {
    STATE.with_borrow(|s| s.view.clone())
}

pub fn text_scale() -> Option<f32> {
    STATE.with_borrow(|s| s.view.is_some().then_some(s.look.text_scale))
}

pub fn hold_text_scale(scale: Option<f32>) {
    STATE.with_borrow_mut(|s| {
        s.held_scale = scale;
        if let Some(scale) = scale.filter(|scale| *scale != s.look.text_scale) {
            s.look.text_scale = scale;
            s.generation += 1;
        }
    });
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
static SCREEN: AtomicU32 = AtomicU32::new(2880);
static CHOSEN: AtomicU64 = AtomicU64::new(0);

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
    s.screen = screen_side();
    SCREEN.store(s.screen, Ordering::Relaxed);
    std::thread::spawn(move || follow(tx));
}

fn redecode(s: &mut State) {
    let side = screen_side();
    if side == s.screen {
        return;
    }
    s.screen = side;
    SCREEN.store(side, Ordering::Relaxed);
    let (Some(tx), paths) = (s.tx.clone(), s.look.images()) else { return };
    let chosen = CHOSEN.load(Ordering::Relaxed);
    std::thread::spawn(move || {
        let Ok(client) = Client::from_env() else { return };
        for path in paths {
            if SCREEN.load(Ordering::Relaxed) != side || CHOSEN.load(Ordering::Relaxed) != chosen {
                return;
            }
            let got = client.ui_background(&path).and_then(|data| fl::decode(&data, side, SAMPLE_SIDE));
            if tx.send(Got::Image(path, side, got)).is_err() {
                return;
            }
        }
    });
}

fn screen_side() -> u32 {
    rune::displays().iter().map(|d| d.size.0.max(d.size.1)).max().unwrap_or(2880)
}

struct Reader {
    class: &'static str,
    device: String,
    decoded_at: u32,
    fetched: Vec<String>,
    tx: Sender<Got>,
}

fn follow(tx: Sender<Got>) {
    let mut client = loop {
        match Client::from_env() {
            Ok(client) => break client,
            Err(e) => {
                if tx.send(Got::Problem(Some(format!("Can't read appearance settings without Agenthail's sign-in ({e}); waiting for it.")))).is_err() {
                    return;
                }
                std::thread::sleep(SIGN_IN_WAIT);
            }
        }
    };
    let device = fl::device_name();
    if tx.send(Got::Device(device.clone())).is_err() {
        return;
    }
    let reader = RefCell::new(Reader { class: fl::device_class(), device, decoded_at: 0, fetched: Vec::new(), tx: tx.clone() });
    let mut cursor: Option<u64> = None;
    let mut reconnect = Reconnect::default();
    loop {
        if let Ok(fresh) = Client::from_env() {
            client = fresh;
        }
        let after = match cursor {
            Some(after) => after,
            None => match client.base_settings() {
                Ok(base) => base.event_cursor,
                Err(f) => {
                    if tx.send(Got::Problem(Some(format!("Couldn't read appearance settings: {}", f.message)))).is_err() {
                        return;
                    }
                    std::thread::sleep(FIRST_READ_RETRY);
                    continue;
                }
            },
        };
        let (open, last, heard) = (Cell::new(true), Cell::new(after), Cell::new(false));
        let result = client.follow_events(
            after,
            || open.set(read(&client, &mut reader.borrow_mut())),
            |kind, id| {
                last.set(last.get().max(id));
                heard.set(true);
                if kind == "ui.changed" || kind == "stream.reset" {
                    open.set(read(&client, &mut reader.borrow_mut()));
                }
                open.get()
            },
        );
        if !open.get() {
            return;
        }
        cursor = Some(last.get());
        if heard.get() {
            reconnect.reset();
        }
        if let Err(e) = result
            && tx.send(Got::Problem(Some(format!("Can't follow appearance changes right now ({e}); reconnecting.")))).is_err()
        {
            return;
        }
        std::thread::sleep(reconnect.next());
    }
}

fn read(client: &Client, r: &mut Reader) -> bool {
    let ticket = ticket();
    let view = match client.ui_view(r.class, &r.device) {
        Ok(view) => view,
        Err(f) => return r.tx.send(Got::Problem(Some(format!("Couldn't read appearance settings: {}", f.message)))).is_ok(),
    };
    let paths = Look::from_view(&view).images();
    let side = SCREEN.load(Ordering::Relaxed);
    if side != r.decoded_at {
        r.decoded_at = side;
        r.fetched.clear();
    }
    r.fetched.retain(|p| paths.contains(p));
    if r.tx.send(Got::View(ticket, Box::new(view))).is_err() {
        return false;
    }
    for path in paths {
        if r.fetched.contains(&path) {
            continue;
        }
        let got = client.ui_background(&path).and_then(|data| fl::decode(&data, side, SAMPLE_SIDE));
        if got.is_ok() {
            r.fetched.push(path.clone());
        }
        if r.tx.send(Got::Image(path, side, got)).is_err() {
            return false;
        }
    }
    true
}

fn picture(d: fl::Decoded) -> Picture {
    let pixels = Pixels::new();
    let (w, h, rgba) = d.shown;
    pixels.set(w, h, rgba);
    Picture { pixels, original: d.original, brightest: d.brightest, darkest: d.darkest, worst: HashMap::new(), see_through: d.see_through }
}

impl Picture {
    fn size(&self, scale: f32, screen: u32) -> (f32, f32) {
        let (w, h) = (self.original.0 as f32, self.original.1 as f32);
        let fit = (screen as f32 / w.max(h).max(1.0)).min(1.0);
        (w * fit / scale.max(1.0), h * fit / scale.max(1.0))
    }

    fn worst(&mut self, bright: bool, blur: f32, shown: (f32, f32)) -> Rgb {
        let sample = if bright { &self.brightest } else { &self.darkest };
        let per_point = (sample.0 as f32 / shown.0.max(1.0)).min(sample.1 as f32 / shown.1.max(1.0));
        let cell = (blur * per_point).round().max(1.0) as usize;
        *self.worst.entry((bright, cell)).or_insert_with(|| fl::worst(&sample.2, sample.0, bright, cell))
    }
}

pub fn release_text_scale() {
    STATE.with_borrow_mut(|s| {
        s.held_scale = None;
        relook(s);
        s.generation += 1;
    });
}

fn relook(s: &mut State) {
    if let Some(v) = &s.view {
        s.look = if s.increased { Look::with_increased_contrast(v) } else { Look::from_view(v) };
        if let Some(scale) = s.held_scale {
            s.look.text_scale = scale;
        }
    }
}

fn pump(s: &mut State) {
    if s.rx.is_none() {
        start(s);
    }
    let got: Vec<Got> = s.rx.as_ref().map(|rx| rx.try_iter().collect()).unwrap_or_default();
    for g in got {
        if matches!(&g, Got::Problem(p) if *p == s.problem) {
            continue;
        }
        s.generation += 1;
        match g {
            Got::Device(d) => s.device = d,
            Got::View(ticket, v) if ticket > s.applied => {
                s.applied = ticket;
                s.view = Some(*v);
                let before = s.look.images();
                relook(s);
                if s.look.images() != before {
                    CHOSEN.fetch_add(1, Ordering::Relaxed);
                }
                redecode(s);
                let keep = s.look.images();
                s.pictures.retain(|p, _| keep.contains(p));
                let meshes = [wallpaper_key(&s.look.background), wallpaper_key(&s.look.background_light)];
                s.meshes.retain(|k, _| meshes.contains(k));
                s.problem = None;
            }
            Got::View(..) => {}
            Got::Image(path, side, _) if side != s.screen || !s.look.images().contains(&path) => {}
            Got::Image(path, _, Ok(d)) => {
                let p = picture(d);
                s.pictures.insert(path, Ok(p));
            }
            Got::Image(path, _, Err(e)) => {
                if !matches!(s.pictures.get(&path), Some(Ok(_))) {
                    s.pictures.insert(path, Err(e));
                }
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

#[cfg(test)]
pub fn generation() -> u64 {
    STATE.with_borrow_mut(|s| {
        pump(s);
        s.generation
    })
}

pub fn tabs() -> feed::tabs::Setting {
    STATE.with_borrow(|s| s.view.as_ref().map(|v| feed::tabs::setting(&v.values)).unwrap_or_default())
}

pub fn poll_seen(seen: &mut u64, tx: &mut Tx) -> bool {
    STATE.with_borrow_mut(|s| {
        pump(s);
        let changed = *seen != s.generation;
        *seen = s.generation;
        if changed && s.view.is_some() {
            tx.set_color_scheme(scheme_of(&s.look));
        }
        changed
    })
}

fn rgb(c: Rgb) -> Color {
    Color { r: fl::linear(c[0]), g: fl::linear(c[1]), b: fl::linear(c[2]), a: 1.0 }
}

fn srgb(c: Color) -> Rgb {
    [fl::gamma(c.r), fl::gamma(c.g), fl::gamma(c.b)]
}

fn text_surfaces(dark: bool) -> [Rgb; 3] {
    if dark { [ground_rgb(true), [0.11, 0.11, 0.125], [0.14, 0.14, 0.16]] } else { [ground_rgb(false), [1.0; 3], [0.93, 0.93, 0.945]] }
}

fn ground_rgb(dark: bool) -> Rgb {
    if dark { [0.086, 0.086, 0.094] } else { [0.957, 0.957, 0.965] }
}

fn wallpaper_key(w: &Wallpaper) -> String {
    format!("{w:?}")
}

fn worst_of(s: &mut State, dark: bool, bright: bool) -> Option<Rgb> {
    let w = s.look.wallpaper(dark).clone();
    let blur = if s.look.translucent(dark) { s.look.blur / 3.0 } else { 0.0 };
    let (area, scale, fit, position, screen) = (s.area, s.scale, s.look.fit, s.look.position, s.screen);
    match &w {
        Wallpaper::Image(path) => s.pictures.get_mut(path).and_then(|p| p.as_mut().ok()).map(|p| {
            let placed = fl::place(fit, position, p.size(scale, screen), area);
            p.worst(bright, blur, (placed.rect[2], placed.rect[3]))
        }),
        Wallpaper::Mesh { .. } => mesh_picture(s, &w).map(|p| p.worst(bright, blur, area)),
        Wallpaper::Linear { stops, .. } => {
            let key = |c: &Rgb| fl::luminance(*c);
            let colors = stops.iter().map(|(c, _)| *c);
            if bright { colors.max_by(|a, b| key(a).total_cmp(&key(b))) } else { colors.min_by(|a, b| key(a).total_cmp(&key(b))) }
        }
        Wallpaper::None | Wallpaper::Desktop => None,
    }
}

fn samples(s: &mut State, dark: bool, ground: Rgb) -> [Rgb; 3] {
    let worst = worst_of(s, dark, dark).unwrap_or(ground);
    let other = worst_of(s, dark, !dark).unwrap_or(worst);
    [worst, other, if exposed(s, dark) { ground } else { worst }]
}

fn exposed(s: &State, dark: bool) -> bool {
    let Wallpaper::Image(path) = s.look.wallpaper(dark) else { return false };
    let Some(Ok(p)) = s.pictures.get(path) else { return true };
    if p.see_through {
        return true;
    }
    let placed = fl::place(s.look.fit, s.look.position, p.size(s.scale, s.screen), s.area);
    let [x, y, w, h] = placed.rect;
    !placed.tile && s.look.fit != Fit::Cover && (x > 0.5 || y > 0.5 || x + w < s.area.0 - 0.5 || y + h < s.area.1 - 0.5)
}

fn mesh_picture<'a>(s: &'a mut State, w: &Wallpaper) -> Option<&'a mut Picture> {
    let Wallpaper::Mesh { columns, rows, colors } = w else { return None };
    let key = wallpaper_key(w);
    if !s.meshes.contains_key(&key) {
        let (mw, mh, rgba) = fl::mesh(*columns, *rows, colors, 1.0);
        let d = fl::Decoded { original: (mw, mh), shown: (mw, mh, rgba.clone()), brightest: (mw, mh, rgba.clone()), darkest: (mw, mh, rgba), see_through: false };
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

const KEPT_KITS: usize = 16;

fn cached(s: &mut State, key: String, build: impl Fn(&KitEnv) -> Kit + 'static) -> KitTheme {
    let hit = match s.kits.iter().position(|(k, _)| *k == key) {
        Some(i) => s.kits.remove(i),
        None => (key, KitTheme::new(build)),
    };
    let theme = hit.1.clone();
    s.kits.insert(0, hit);
    s.kits.truncate(KEPT_KITS);
    theme
}

fn main_kit(env: &KitEnv, paint: &Paint, roles: TextRoles, translucent: bool, behinds: Backdrops) -> Kit {
    let neutral = Kit::neutral(paint.dark);
    let mut k = if env.increased_contrast { neutral.increased_contrast() } else { neutral };
    if translucent {
        k.c.bg_deep = paint.pane;
        k.c.bg_base = WASH;
        k.c.border_muted = paint.border;
    }
    k.c.text_base = paint.text;
    k.c.text_muted = paint.muted;
    k.c.text_faint = paint.faint;
    accented(&mut k, paint.accent);
    k.roles = roles;
    hold_kit(k, behinds)
}

fn chrome_kit(env: &KitEnv, paint: &Paint, roles: TextRoles, translucent: bool, drawn: bool, behinds: Backdrops) -> Kit {
    let mut k = rune_theme_opencode::kit(&env.dark(paint.dark));
    if translucent {
        k.c.bg_deep = paint.pane;
        k.c.bg_base = WASH;
        k.c.border_muted = paint.border;
    } else if paint.dark {
        k.c.bg_deep = if drawn { paint.card_deep } else { Color::TRANSPARENT };
        k.c.border_muted = Color::WHITE.alpha(0.09);
    }
    k.c.text_strong = paint.text;
    k.c.text_base = paint.text;
    k.c.text_weak = paint.muted;
    accented(&mut k, paint.accent);
    k.roles = roles;
    hold_kit(k, behinds)
}

fn kit_key(paint: &Paint, roles: &TextRoles, translucent: bool, drawn: bool, behinds: Backdrops) -> String {
    let q = |c: Color| [quant(fl::gamma(c.r)), quant(fl::gamma(c.g)), quant(fl::gamma(c.b)), quant(c.a)];
    let colors: Vec<[u32; 4]> = [paint.text, paint.muted, paint.faint, paint.accent, paint.pane, paint.border, paint.card_deep].into_iter().chain(behinds).chain(rune_kit::TextRole::ALL.iter().map(|r| roles.get(*r))).map(q).collect();
    format!("{}:{translucent}:{drawn}:{colors:?}", paint.dark)
}

fn accented(k: &mut Kit, a: Color) {
    k.c.bg_accent = a;
    k.c.text_accent = a;
    k.c.icon_accent = a;
    k.c.border_focus = a;
}

pub fn frame<S, R>(cx: &mut Cx<S>, session: bool, glass: bool, build: impl FnOnce(&mut Cx<S>, &theme::Themes) -> R) -> R {
    let system_dark = cx.system_dark();
    let system_accent = srgb(cx.accent());
    let increased = cx.system_increased_contrast();
    let (paint, kit, chrome) = STATE.with_borrow_mut(|s| {
        if increased != s.increased {
            s.increased = increased;
            relook(s);
        }
        let dark = s.look.dark(system_dark);
        let mut paint = if dark { Paint::dark() } else { Paint::light() };
        let accent_rgb = s.look.accent.map(|(d, l)| if dark { d } else { l }).unwrap_or_else(|| fl::readable_text(system_accent, &text_surfaces(dark), dark, s.look.target));
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
        let mut roles = TextRoles {
            meta: role("meta", paint.faint),
            link: role("link", paint.accent),
            code: role("code", paint.text),
            working: role("working", paint.accent),
            added: role("done", paint.done),
            removed: role("diff.removed", paint.failed),
            peer: role("peer", paint.text),
            needs_attention: paint.attention,
            done: paint.done,
            error: paint.failed,
        };
        let translucent = glass && s.look.translucent(dark);
        let drawn = s.look.drawn(dark);
        let behinds = if translucent {
            let ground = ground_rgb(dark);
            let tint = tint_rgb(&s.look, dark, accent_rgb);
            let strength = if session { s.look.session_opacity } else { s.look.opacity };
            let [worst, other, bare] = samples(s, dark, ground);
            let (pane, pane_rgb) = s.look.pane_tint(dark, accent_rgb, worst, ground, tint, strength, s.look.pane_opacity);
            let (float, float_rgb) = s.look.pane_tint(dark, accent_rgb, worst, ground, tint, strength, s.look.float_opacity);
            paint.card = paint.card.alpha(0.6);
            paint.card_deep = paint.card_deep.alpha(0.7);
            paint.raised = paint.raised.alpha(0.78);
            paint.glass_tint = rgb(float_rgb).alpha(float);
            paint.blur = s.look.blur / 3.0;
            paint.ground = rgb(ground);
            paint.pane = rgb(pane_rgb).alpha(pane);
            paint.border = Color::WHITE.alpha(s.look.border);
            let ink = if dark { [0.0; 3] } else { [1.0; 3] };
            [worst, other, bare].map(|w| rgb(fl::blend(fl::blend(ground, w, strength), ink, s.look.dim)))
        } else {
            paint.glass_tint = paint.card.alpha(if dark { 0.78 } else { 1.0 });
            [paint.card_deep; 3]
        };
        let floors: Vec<Color> = if translucent {
            behinds.iter().flat_map(|&b| {
                let pane = over(paint.pane, b);
                [pane, over(WASH, pane)]
            }).collect()
        } else {
            behinds.to_vec()
        };
        hold_paint(&mut paint, &mut roles, &floors, s.look.target.max(if increased { 7.0 } else { 4.5 }), increased);
        let key = kit_key(&paint, &roles, translucent, drawn, behinds);
        let kit = cached(s, format!("kit:{key}"), move |env| main_kit(env, &paint, roles, translucent, behinds));
        let chrome = cached(s, format!("chrome:{key}"), move |env| chrome_kit(env, &paint, roles, translucent, drawn, behinds));
        (paint, kit, chrome)
    });
    let themes = theme::Themes { main: kit, chrome };
    theme::set(paint, themes.clone());
    with_text(cx, |cx| cx.with_kit(&themes.main, |cx| build(cx, &themes)))
}

pub fn with_text<S, R>(cx: &mut Cx<S>, build: impl FnOnce(&mut Cx<S>) -> R) -> R {
    let text = STATE.with_borrow_mut(|s| {
        if (s.text.get() - s.look.text_scale).abs() > 1e-6 {
            s.text = TextScale::new(s.look.text_scale);
        }
        s.text.clone()
    });
    cx.with_text_scale(&text, build)
}

pub fn backdrop(session: bool) -> El {
    STATE.with_borrow_mut(|s| {
        let paint = theme::paint();
        let dark = paint.dark;
        let base = el().key("backdrop").absolute(0.0, 0.0).w_full().h_full().on_layout_any(|r, tx| {
            let scale = tx.scale();
            STATE.with_borrow_mut(|s| {
                s.area = (r[2], r[3]);
                if s.scale != scale {
                    s.scale = scale;
                    redecode(s);
                }
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
                    let placed = fl::place(s.look.fit, s.look.position, p.size(s.scale, s.screen), s.area);
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

#[path = "look_legible.rs"]
mod legible;

#[cfg(test)]
#[path = "look_tests.rs"]
mod tests;
