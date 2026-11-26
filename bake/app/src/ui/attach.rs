use std::collections::{HashMap, HashSet, VecDeque};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use feed::attach::{self, FileKind, Prepared, Problem, State};
use feed::blocks::Kind;
use feed::item::{Attachment, AttachedFile, Item};
use rune::image::ImageCache;
use rune::{ClipboardContent, Color, El, FileDialogOptions, Icon, PointerPress, SurfaceFit, Tx, Waker, col, el, icon, row, text};
use rune_kit::{Menu, MenuItem};

use crate::icons;
use crate::theme::{self, META, META_BOLD, failed, faint, line, muted, wash};
use crate::{App, Bg};

const PICTURES_AT_ONCE: usize = 3;
const COPIED_FOR: Duration = Duration::from_secs(2);
const IMAGE_WIDTH: f32 = 520.0;
const IMAGE_HEIGHT: f32 = 360.0;
const PLACEHOLDER_RATIO: f32 = 220.0 / 140.0;

pub enum Done {
    Prepared(u64, Vec<Result<Prepared, Problem>>),
    Uploaded(u64, Result<attach::Upload, String>),
    Picture(String, Result<Vec<u8>, String>),
    Fetched(String, Then, Result<PathBuf, String>),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Then {
    Open,
    Reveal,
}

pub enum Picture {
    Loading,
    Ready(rune::Image),
    Failed(String),
}

#[derive(Default)]
pub struct Sent {
    pub loading: bool,
    pub failure: Option<String>,
    pub copied: Option<Instant>,
}

#[derive(Clone)]
pub enum Target {
    Image { session: String, attachment: Attachment },
    File { session: String, item: Item },
    Path { key: String, path: String },
}

pub struct Open {
    pub target: Target,
    pub at: PointerPress,
}

#[derive(Default)]
pub struct Attach {
    pub tray: attach::Tray,
    pub cache: Option<ImageCache>,
    pub pictures: HashMap<String, Picture>,
    pub sent: HashMap<String, Sent>,
    pub thumbs: HashMap<u64, rune::Image>,
    pub adding: HashMap<u64, (String, Vec<String>)>,
    next_job: u64,
    pub menu: Option<Open>,
    pub dropping: bool,
    pub rev: u64,
    wanted: VecDeque<(String, Attachment)>,
    fetching: usize,
    scanned: u64,
    rescan: bool,
}

pub fn setup(s: &mut App, waker: Waker) {
    s.attach.cache = Some(ImageCache::new(waker));
}

fn cache_root() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join("Library/Caches/Agenthail/SentFiles"))
}

fn cache_path(id: &str, name: &str) -> Option<PathBuf> {
    Some(cache_root()?.join(attach::folder(id)).join(attach::safe_name(name, "file")))
}

fn picture_name(a: &Attachment) -> String {
    a.name.clone().filter(|n| !n.trim().is_empty()).unwrap_or_else(|| {
        let ext = match a.media_type.as_str() {
            "image/jpeg" => "jpg",
            "image/gif" => "gif",
            "image/webp" => "webp",
            _ => "png",
        };
        format!("image.{ext}")
    })
}

fn cached(id: &str, name: &str) -> Option<PathBuf> {
    cache_path(id, name).filter(|p| p.is_file())
}

fn keep(id: &str, name: &str, data: &[u8]) -> Result<PathBuf, String> {
    let target = cache_path(id, name).ok_or("HOME is not set")?;
    let folder = target.parent().ok_or("no cache folder")?.to_path_buf();
    std::fs::create_dir_all(&folder).map_err(|e| e.to_string())?;
    let next = folder.join(format!(".{}.part", attach::safe_name(name, "file")));
    std::fs::write(&next, data).map_err(|e| e.to_string())?;
    std::fs::rename(&next, &target).map_err(|e| e.to_string())?;
    let stamp = format!("0081;{:08x};Agenthail;", std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or_default());
    let marked = std::process::Command::new("/usr/bin/xattr").args(["-w", "com.apple.quarantine", &stamp]).arg(&target).status().is_ok_and(|s| s.success());
    if !marked {
        let _ = std::fs::remove_dir_all(&folder);
        return Err("The file couldn't be marked as downloaded, so it wasn't kept.".into());
    }
    Ok(target)
}

pub fn can_attach(s: &App, id: &str) -> bool {
    crate::session(s, id).is_some_and(|x| x.capabilities.attach && !x.read_only)
}

fn target(s: &App) -> Option<String> {
    crate::over::composer_target(s).filter(|id| can_attach(s, id))
}

pub fn has(s: &App, id: &str) -> bool {
    s.attach.tray.has(id) || adding(s, id) > 0
}

pub fn adding(s: &App, id: &str) -> usize {
    s.attach.adding.values().filter(|(at, _)| at == id).count()
}

pub fn saved(s: &App) -> serde_json::Value {
    let names: Vec<(String, String)> = s.attach.adding.values().flat_map(|(id, names)| names.iter().map(move |n| (id.clone(), n.clone()))).collect();
    s.attach.tray.to_json_with(&names)
}

pub fn rekey(s: &mut App, old: &str, new: &str) {
    for (id, _) in s.attach.adding.values_mut() {
        if id == old {
            *id = new.to_string();
        }
    }
}

pub fn ready(s: &App, id: &str) -> Result<Vec<attach::Upload>, String> {
    if adding(s, id) > 0 {
        return Err(attach::Hold::Uploading.note());
    }
    s.attach.tray.ready(id).map_err(|hold| hold.note())
}

pub fn add_paths(s: &mut App, id: &str, paths: Vec<PathBuf>) {
    if paths.is_empty() {
        return;
    }
    let names = paths.iter().map(|p| p.file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_else(|| "file".into())).collect();
    prepare(s, id, names, move || paths.iter().map(|p| attach::file(p)).collect());
}

fn add_picture(s: &mut App, id: &str, name: &str, png: Vec<u8>) {
    let name = name.to_string();
    prepare(s, id, vec![name.clone()], move || vec![attach::picture(&name, &png)]);
}

fn prepare(s: &mut App, id: &str, names: Vec<String>, work: impl FnOnce() -> Vec<Result<Prepared, Problem>> + Send + 'static) {
    let id = feed::composer::current(&s.rekeyed, id).to_string();
    s.attach.next_job += 1;
    let job = s.attach.next_job;
    s.attach.adding.insert(job, (id.clone(), names));
    keep_draft(s, &id);
    let out = s.outbox.clone();
    std::thread::spawn(move || {
        let _ = out.send(Bg::Attach(Done::Prepared(job, work())));
    });
}

fn keep_draft(s: &mut App, id: &str) {
    if let Err(error) = crate::over::layout::save_now(s) {
        crate::note(s, id, format!("The draft couldn't be saved ({error}), so its attachments may not come back if the app closes."));
    }
}

pub fn choose(s: &mut App, tx: &mut Tx) {
    let Some(id) = target(s) else { return };
    let options = FileDialogOptions { title: "Attach files or images".into(), ..Default::default() };
    tx.open_files(options, move |s: &mut App, result, _| match result {
        Ok(Some(paths)) => add_paths(s, &id, paths),
        Ok(None) => {}
        Err(error) => crate::note(s, &id, format!("Couldn't open the file picker: {error}")),
    });
}

pub fn dropped(s: &mut App, paths: &[PathBuf]) {
    s.attach.dropping = false;
    if let Some(id) = target(s) {
        add_paths(s, &id, paths.to_vec());
    }
}

pub fn drop_target(view: El) -> El {
    view.on_drop_files(|s: &mut App, paths, _: &mut Tx| dropped(s, paths))
        .accepts_drag(|s: &App, _| target(s).is_some())
        .on_drop_hover(|s: &mut App, over, _: &mut Tx| s.attach.dropping = over)
}

pub fn paste(s: &mut App, content: &ClipboardContent) -> bool {
    let Some(id) = target(s) else { return false };
    match attach::paste(content.files().len(), content.image().is_some(), content.text()) {
        attach::Paste::Files => add_paths(s, &id, content.files().to_vec()),
        attach::Paste::Image => match content.image() {
            Some(image) => add_picture(s, &id, "Pasted image.png", image.png().to_vec()),
            None => return false,
        },
        attach::Paste::Text => return false,
    }
    true
}

fn upload(s: &mut App, id: &str, local: u64, name: String, data: std::sync::Arc<[u8]>) {
    let Some(client) = s.client.clone() else {
        s.attach.tray.uploaded(local, Err("Fixture mode: not uploaded".into()));
        return;
    };
    let (out, session) = (s.outbox.clone(), id.to_string());
    std::thread::spawn(move || {
        let result = client.upload(&session, &name, &data).map_err(|e| e.message);
        let _ = out.send(Bg::Attach(Done::Uploaded(local, result)));
    });
}

pub fn clear(s: &mut App, id: &str) {
    for chip in s.attach.tray.chips(id) {
        s.attach.thumbs.remove(&chip.local);
    }
    s.attach.tray.clear(id);
}

pub fn restore(s: &mut App, id: &str, unsent: &feed::composer::Unsent) {
    s.attach.tray.restore(id, &unsent.attachments);
    if let Some(gone) = attach::missing_upload(&unsent.error) {
        s.attach.tray.expire(id, &gone);
    }
}

pub fn remove(s: &mut App, local: u64) {
    s.attach.tray.remove(local);
    s.attach.thumbs.remove(&local);
}

pub fn retry_upload(s: &mut App, id: &str, local: u64) {
    if let Some((name, data)) = s.attach.tray.retry(local) {
        upload(s, id, local, name, data);
    }
}

pub fn done(s: &mut App, done: Done, tx: &mut Tx) {
    s.attach.rev += 1;
    match done {
        Done::Prepared(job, files) => {
            let Some((id, _)) = s.attach.adding.remove(&job) else { return };
            let id = feed::composer::current(&s.rekeyed, &id).to_string();
            let mut problem = None;
            for file in files {
                let prepared = match file {
                    Ok(prepared) => prepared,
                    Err(error) => {
                        problem.get_or_insert(error);
                        continue;
                    }
                };
                let (name, data, image) = (prepared.name.clone(), prepared.data.clone(), prepared.is_image());
                match s.attach.tray.add(&id, prepared) {
                    Ok(Some(local)) => {
                        if image && let Some(cache) = &s.attach.cache {
                            s.attach.thumbs.insert(local, cache.load_scaled(data.clone(), 18.0, 2.0));
                        }
                        upload(s, &id, local, name, data);
                    }
                    Ok(None) => {}
                    Err(error) => {
                        problem.get_or_insert(error);
                    }
                }
            }
            if let Some(problem) = problem {
                crate::note(s, &id, problem.message());
            }
            keep_draft(s, &id);
        }
        Done::Uploaded(local, result) => s.attach.tray.uploaded(local, result),
        Done::Picture(id, result) => {
            let state = match (result, &s.attach.cache) {
                (Ok(data), Some(cache)) => Picture::Ready(cache.load_scaled(data, IMAGE_WIDTH, 2.0)),
                (Ok(_), None) => Picture::Failed("Couldn't load image".into()),
                (Err(error), _) => Picture::Failed(error),
            };
            s.attach.fetching = s.attach.fetching.saturating_sub(1);
            s.attach.pictures.insert(id, state);
        }
        Done::Fetched(key, then, result) => {
            let sent = s.attach.sent.entry(key).or_default();
            sent.loading = false;
            match result {
                Ok(path) => {
                    sent.failure = None;
                    act(tx, then, &path);
                }
                Err(error) => sent.failure = Some(error),
            }
        }
    }
}

fn act(tx: &mut Tx, then: Then, path: &Path) {
    let _ = match then {
        Then::Open => tx.open_path(path),
        Then::Reveal => tx.reveal_path(path),
    };
}

pub fn pump(s: &mut App) -> bool {
    let mut changed = false;
    if s.attach.scanned != s.counter || s.attach.rescan {
        s.attach.scanned = s.counter;
        s.attach.rescan = false;
        let mut seen = HashSet::new();
        for (id, pane) in &s.panes {
            for block in pane.blocks.iter().rev() {
                if let Kind::Image(a) = &block.kind
                    && seen.insert(a.id.clone())
                    && !s.attach.pictures.contains_key(&a.id)
                {
                    s.attach.wanted.push_back((id.clone(), a.clone()));
                }
            }
        }
        s.attach.pictures.retain(|id, p| seen.contains(id) || matches!(p, Picture::Loading));
        for (_, a) in &s.attach.wanted {
            s.attach.pictures.insert(a.id.clone(), Picture::Loading);
        }
    }
    while s.attach.fetching < PICTURES_AT_ONCE
        && let Some((session, a)) = s.attach.wanted.pop_front()
    {
        changed = true;
        s.attach.rev += 1;
        let Some(client) = s.client.clone() else {
            s.attach.pictures.insert(a.id.clone(), Picture::Failed("Couldn't load image".into()));
            continue;
        };
        s.attach.fetching += 1;
        let out = s.outbox.clone();
        std::thread::spawn(move || {
            let name = picture_name(&a);
            let result = match std::fs::read(cached(&a.id, &name).unwrap_or_default()) {
                Ok(data) => Ok(data),
                Err(_) => client.attachment(&session, &a.id).inspect(|data| {
                    let _ = keep(&a.id, &name, data);
                }),
            };
            let _ = out.send(Bg::Attach(Done::Picture(a.id, result)));
        });
    }
    let broken: Vec<String> = s.attach.pictures.iter().filter_map(|(id, p)| matches!(p, Picture::Ready(image) if image.error().is_some()).then(|| id.clone())).collect();
    for id in broken {
        s.attach.pictures.insert(id, Picture::Failed("Couldn't load image".into()));
        s.attach.rev += 1;
        changed = true;
    }
    let now = Instant::now();
    for sent in s.attach.sent.values_mut() {
        if sent.copied.is_some_and(|at| now.duration_since(at) >= COPIED_FOR) {
            sent.copied = None;
            s.attach.rev += 1;
            changed = true;
        }
    }
    changed
}

fn retry_picture(s: &mut App, id: &str) {
    if let Some(folder) = cache_root().map(|root| root.join(attach::folder(id))) {
        let _ = std::fs::remove_dir_all(folder);
    }
    s.attach.pictures.remove(id);
    s.attach.rescan = true;
    s.attach.rev += 1;
}

fn fetch_sent(s: &mut App, session: &str, item: &Item, then: Then, tx: &mut Tx) {
    if let Some(a) = &item.attachment {
        fetch(s, &item.id, session, &a.id, &attach::sent_name(item), then, tx);
    }
}

fn open_picture(s: &mut App, session: &str, a: &Attachment, then: Then, tx: &mut Tx) {
    fetch(s, &a.id, session, &a.id, &picture_name(a), then, tx);
}

fn fetch(s: &mut App, key: &str, session: &str, id: &str, name: &str, then: Then, tx: &mut Tx) {
    if let Some(path) = cached(id, name) {
        act(tx, then, &path);
        return;
    }
    let sent = s.attach.sent.entry(key.to_string()).or_default();
    if sent.loading {
        return;
    }
    let Some(client) = s.client.clone() else {
        sent.failure = Some("Not available without the daemon".into());
        return;
    };
    sent.loading = true;
    s.attach.rev += 1;
    let (out, session, key, id, name) = (s.outbox.clone(), session.to_string(), key.to_string(), id.to_string(), name.to_string());
    std::thread::spawn(move || {
        let result = client.attachment(&session, &id).and_then(|data| keep(&id, &name, &data));
        let _ = out.send(Bg::Attach(Done::Fetched(key, then, result)));
    });
}

fn copy_path(s: &mut App, key: &str, path: &str, tx: &mut Tx) {
    tx.copy(path.to_string());
    s.attach.sent.entry(key.to_string()).or_default().copied = Some(Instant::now());
    s.attach.rev += 1;
}

fn run(s: &mut App, target: Target, act_: &'static str, tx: &mut Tx) {
    s.attach.menu = None;
    match (target, act_) {
        (Target::Image { session, attachment }, "open") => open_picture(s, &session, &attachment, Then::Open, tx),
        (Target::Image { session, attachment }, "reveal") => open_picture(s, &session, &attachment, Then::Reveal, tx),
        (Target::Image { attachment, .. }, "copy") => {
            if let Some(Picture::Ready(image)) = s.attach.pictures.get(&attachment.id) {
                tx.copy_image(image);
            }
        }
        (Target::File { session, item }, "open") => fetch_sent(s, &session, &item, Then::Open, tx),
        (Target::File { session, item }, "reveal") => fetch_sent(s, &session, &item, Then::Reveal, tx),
        (Target::File { item, .. }, "copy") => {
            let path = item.files.first().map(|f| f.path.clone()).unwrap_or_default();
            copy_path(s, &item.id, &path, tx);
        }
        (Target::Path { key, path }, "copy") => copy_path(s, &key, &path, tx),
        _ => {}
    }
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    let Some(open) = &s.attach.menu else { return };
    let mut m = Menu::new("attachment-menu").open(true).min_width(200.0).at(open.at.x, open.at.y).on_close(|s: &mut App| s.attach.menu = None);
    let entries: Vec<(&str, &'static str, bool)> = match &open.target {
        Target::Image { attachment, .. } => {
            let ready = matches!(s.attach.pictures.get(&attachment.id), Some(Picture::Ready(_)));
            vec![("Open in Preview", "open", ready), ("Show in Finder", "reveal", ready), ("Copy Image", "copy", ready)]
        }
        Target::File { session, item } => {
            let host = crate::session(s, session).map(|x| x.host.name.clone());
            let opens = attach::sent_reason(item, host.as_deref()).is_none();
            let path = item.files.first().is_some_and(|f| !f.path.is_empty());
            vec![("Open", "open", opens), ("Show in Finder", "reveal", opens), ("Copy Path", "copy", path)]
        }
        Target::Path { .. } => vec![("Copy Path", "copy", true)],
    };
    for (label, act_, enabled) in entries {
        let target = open.target.clone();
        m = m.item(MenuItem::new(label).disabled(!enabled).on_select_tx(move |s: &mut App, tx: &mut Tx| run(s, target.clone(), act_, tx)));
    }
    let _ = m.build(theme::kit(), layer);
}

fn menu_at(target: Target) -> impl Fn(&mut App, PointerPress, &mut Tx) + 'static {
    move |s: &mut App, at, _: &mut Tx| s.attach.menu = Some(Open { target: target.clone(), at })
}

fn kind_icon(kind: FileKind) -> &'static Icon {
    match kind {
        FileKind::Pdf | FileKind::Text => &icons::FILE_TEXT,
        FileKind::Image => &icons::IMAGE,
        FileKind::Movie => &icons::FILM,
        FileKind::Audio => &icons::AUDIO,
        FileKind::Archive => &icons::ARCHIVE,
        FileKind::Other => &icons::FILE,
    }
}

pub fn button(s: &App, id: &str) -> Option<El> {
    can_attach(s, id).then(|| {
        col()
            .key("attach")
            .size(30.0, 30.0)
            .radius(9.0)
            .items_center()
            .justify_center()
            .hover(Color::WHITE.alpha(0.08))
            .label("Attach files or images")
            .help("Attach files or images")
            .on_press_tx(choose)
            .child(icon(&icons::PAPERCLIP, 15.0).color(muted()))
    })
}

pub fn chips(s: &App, id: &str) -> Option<El> {
    let list = s.attach.tray.chips(id);
    let preparing = adding(s, id);
    if list.is_empty() && preparing == 0 {
        return None;
    }
    let mut r = row().key("attachment-chips").w_full().flex_wrap().gap(6.0);
    for chip in list {
        let local = chip.local;
        let failed_ = matches!(chip.state, State::Failed(_) | State::Gone(_));
        let thumb = match s.attach.thumbs.get(&local).filter(|_| chip.image) {
            Some(image) => el().size(18.0, 18.0).radius(4.0).pixels_fit(image.pixels(), SurfaceFit::Fill),
            None => col().size(18.0, 18.0).items_center().justify_center().child(icon(if chip.image { &icons::IMAGE } else { &icons::FILE }, 12.0).color(faint())),
        };
        let mut c = row()
            .key(format!("chip-{local}"))
            .gap(6.0)
            .items_center()
            .pl(3.0)
            .pr(5.0)
            .h(24.0)
            .radius(6.0)
            .fill(wash())
            .border(1.0, if failed_ { failed().alpha(0.6) } else { line() })
            .help(chip.help())
            .child(thumb)
            .child(text(chip.name.clone()).font(META).color(theme::text()).truncate().max_w(220.0));
        match chip.state {
            State::Uploading => c = c.child(rune_kit::Loader::new().size(10.0).color(faint()).build(theme::kit())),
            State::Failed(_) if chip.data.is_some() => {
                let session = id.to_string();
                c = c.child(
                    col()
                        .key(format!("chip-retry-{local}"))
                        .size(16.0, 16.0)
                        .items_center()
                        .justify_center()
                        .label(format!("Upload {} again", chip.name))
                        .on_press(move |s: &mut App| retry_upload(s, &session, local))
                        .child(icon(&icons::ROTATE, 11.0).color(failed())),
                )
            }
            State::Ready(_) | State::Failed(_) | State::Gone(_) => {}
        }
        c = c.child(
            col()
                .key(format!("chip-remove-{local}"))
                .size(16.0, 16.0)
                .radius(4.0)
                .items_center()
                .justify_center()
                .hover(Color::WHITE.alpha(0.08))
                .label(format!("Remove {}", chip.name))
                .on_press(move |s: &mut App| remove(s, local))
                .child(icon(&icons::CLOSE, 10.0).color(muted())),
        );
        r = r.child(c);
    }
    if preparing > 0 {
        r = r.child(row().h(24.0).gap(6.0).items_center().child(rune_kit::Loader::new().size(10.0).color(faint()).build(theme::kit())).child(text("Adding…").font(META).color(faint())));
    }
    Some(r)
}

pub fn image_el(s: &App, session: &str, a: &Attachment) -> El {
    let picture = s.attach.pictures.get(&a.id);
    let decoded = match picture {
        Some(Picture::Ready(image)) => image.size().filter(|[w, h]| *w > 0 && *h > 0).map(|[w, h]| w as f32 / h as f32),
        _ => None,
    };
    let ratio = a.ratio().or(decoded).unwrap_or(PLACEHOLDER_RATIO).clamp(0.5, 4.0);
    let width = IMAGE_WIDTH.min(IMAGE_HEIGHT * ratio);
    let frame = col().key(format!("image-{}", a.id)).size(width, width / ratio).radius(10.0).fill(wash()).border(1.0, line()).items_center().justify_center();
    let body = match picture {
        Some(Picture::Ready(image)) => {
            let (open, from, target) = (a.clone(), session.to_string(), Target::Image { session: session.to_string(), attachment: a.clone() });
            frame
                .pixels_fit(image.pixels(), SurfaceFit::Fit)
                .label("Image")
                .help("Double-click to open")
                .on_double_press_tx(move |s: &mut App, tx: &mut Tx| open_picture(s, &from, &open, Then::Open, tx))
                .on_secondary_press(menu_at(target))
        }
        Some(Picture::Failed(_)) => {
            let id = a.id.clone();
            frame.child(
                col()
                    .gap(2.0)
                    .items_center()
                    .child(text("Couldn't load image").font(META).color(muted()))
                    .child(row().key(format!("image-retry-{}", a.id)).px(6.0).py(2.0).radius(5.0).hover(wash()).on_press(move |s: &mut App| retry_picture(s, &id)).child(text("Retry").font(META_BOLD).color(theme::accent()))),
            )
        }
        _ => frame.child(rune_kit::Loader::new().size(14.0).color(faint()).build(theme::kit())),
    };
    row().w_full().child(body)
}

pub fn file_el(s: &App, session: &str, item: &Item) -> El {
    let name = attach::sent_name(item);
    let host = crate::session(s, session).map(|x| x.host.name.clone());
    let reason = attach::sent_reason(item, host.as_deref());
    let sent = s.attach.sent.get(&item.id);
    let media = item.attachment.as_ref().map(|a| a.media_type.as_str()).unwrap_or_default();
    let detail = if sent.is_some_and(|x| x.copied.is_some()) {
        "Path copied".to_string()
    } else {
        reason.clone().or_else(|| sent.and_then(|x| x.failure.clone())).unwrap_or_else(|| attach::sent_detail(item))
    };
    let mut card = row()
        .key(format!("sent-file-{}", item.id))
        .w_full()
        .max_w(420.0)
        .gap(10.0)
        .items_center()
        .px(12.0)
        .py(8.0)
        .radius(10.0)
        .fill(wash())
        .border(1.0, line())
        .label(if reason.is_none() { format!("Open {name}") } else { name.clone() })
        .child(col().w(28.0).items_center().child(icon(kind_icon(attach::file_kind(&name, media)), 20.0).color(muted())))
        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text(name.clone()).font(META_BOLD).color(theme::text()).truncate()).child(text(detail).font(META).color(faint()).wrap().max_lines(2)));
    if sent.is_some_and(|x| x.loading) {
        card = card.child(rune_kit::Loader::new().size(12.0).color(faint()).build(theme::kit()));
    }
    let target = Target::File { session: session.to_string(), item: item.clone() };
    card = card.on_secondary_press(menu_at(target));
    if reason.is_none() {
        let (session, item) = (session.to_string(), item.clone());
        card = card.hover(Color::WHITE.alpha(0.08)).help(format!("Open {name}")).on_press_tx(move |s: &mut App, tx: &mut Tx| fetch_sent(s, &session, &item, Then::Open, tx));
    } else if item.files.first().is_some_and(|f| !f.path.is_empty()) {
        let (key, path) = (item.id.clone(), item.files[0].path.clone());
        card = card.help(reason.unwrap_or_default()).child(row().key(format!("copy-path-{}", item.id)).px(6.0).py(2.0).radius(5.0).hover(Color::WHITE.alpha(0.08)).on_press_tx(move |s: &mut App, tx: &mut Tx| copy_path(s, &key, &path, tx)).child(text("Copy Path").font(META).color(theme::accent())));
    }
    row().w_full().child(card)
}

pub fn files_el(s: &App, block: &str, files: &[AttachedFile]) -> El {
    let mut r = row().w_full().flex_wrap().justify_end().gap(6.0);
    for (i, file) in files.iter().enumerate() {
        let key = format!("{block}-{i}");
        let copied = s.attach.sent.get(&key).is_some_and(|x| x.copied.is_some());
        let label = match (copied, file.bytes) {
            (true, _) => "Path copied".to_string(),
            (false, Some(bytes)) => attach::size_label(bytes),
            (false, None) => String::new(),
        };
        let mut chip = row()
            .key(format!("attached-{key}"))
            .gap(6.0)
            .items_center()
            .px(8.0)
            .h(26.0)
            .radius(7.0)
            .fill(wash())
            .border(1.0, line())
            .help(file.path.clone())
            .child(icon(kind_icon(attach::file_kind(&file.name, "")), 13.0).color(muted()))
            .child(text(file.name.clone()).font(META).color(theme::text()).truncate().max_w(240.0));
        if !label.is_empty() {
            chip = chip.child(text(label).font(META).color(faint()));
        }
        if !file.path.is_empty() {
            chip = chip.on_secondary_press(menu_at(Target::Path { key, path: file.path.clone() }));
        }
        r = r.child(chip);
    }
    r
}
