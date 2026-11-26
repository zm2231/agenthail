use std::collections::HashMap;
use std::io::Cursor;
use std::path::Path;
use std::sync::Arc;

use image::{DynamicImage, ImageDecoder, ImageFormat, ImageReader};
use serde::Deserialize;
use serde_json::{Value, json};

use crate::item::Item;

pub const MAX_COUNT: usize = 8;
pub const MAX_FILE_BYTES: u64 = 25 << 20;
pub const MAX_IMAGE_BYTES: u64 = 5 << 20;
pub const MAX_IMAGE_EDGE: u32 = 2048;
pub const OPEN_LIMIT: u64 = 50 << 20;

#[derive(Deserialize, Clone, Debug, Default, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Upload {
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub media_type: String,
    #[serde(default)]
    pub bytes: u64,
    #[serde(default)]
    pub width: u32,
    #[serde(default)]
    pub height: u32,
}

impl Upload {
    pub fn to_json(&self) -> Value {
        json!({"id": self.id, "name": self.name, "mediaType": self.media_type, "bytes": self.bytes, "width": self.width, "height": self.height})
    }

    pub fn from_json(value: &Value) -> Option<Upload> {
        serde_json::from_value::<Upload>(value.clone()).ok().filter(|u| !u.id.trim().is_empty())
    }

    pub fn is_image(&self) -> bool {
        self.media_type.starts_with("image/")
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Problem {
    Folder,
    TooLarge(String),
    Unreadable(String),
    TooMany,
}

impl Problem {
    pub fn message(&self) -> String {
        match self {
            Problem::Folder => "Folders can't be attached.".into(),
            Problem::TooLarge(name) => format!("{name} is larger than 25 MB."),
            Problem::Unreadable(name) => format!("{name} couldn't be read."),
            Problem::TooMany => format!("A message can carry up to {MAX_COUNT} attachments."),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Prepared {
    pub name: String,
    pub media_type: String,
    pub data: Arc<[u8]>,
}

impl Prepared {
    pub fn is_image(&self) -> bool {
        IMAGE_TYPES.contains(&self.media_type.as_str())
    }
}

const IMAGE_TYPES: [&str; 3] = ["image/png", "image/jpeg", "image/gif"];

fn kept_type(format: ImageFormat) -> Option<&'static str> {
    match format {
        ImageFormat::Png => Some("image/png"),
        ImageFormat::Jpeg => Some("image/jpeg"),
        ImageFormat::Gif => Some("image/gif"),
        _ => None,
    }
}

// The daemon takes only PNG, JPEG and GIF up to 5 MB, so anything else becomes an upright JPEG.
pub fn image(name: &str, data: &[u8]) -> Option<Prepared> {
    let reader = ImageReader::new(Cursor::new(data)).with_guessed_format().ok()?;
    let format = reader.format()?;
    let mut decoder = reader.into_decoder().ok()?;
    let (width, height) = decoder.dimensions();
    let orientation = decoder.orientation().ok()?;
    let fits = data.len() as u64 <= MAX_IMAGE_BYTES && width.max(height) <= MAX_IMAGE_EDGE && orientation == image::metadata::Orientation::NoTransforms;
    if let Some(media) = kept_type(format).filter(|_| fits) {
        return Some(Prepared { name: name.to_string(), media_type: media.into(), data: data.into() });
    }
    let mut picture = DynamicImage::from_decoder(decoder).ok()?;
    picture.apply_orientation(orientation);
    if picture.width().max(picture.height()) > MAX_IMAGE_EDGE {
        picture = picture.resize(MAX_IMAGE_EDGE, MAX_IMAGE_EDGE, image::imageops::FilterType::Triangle);
    }
    let mut out = Vec::new();
    image::codecs::jpeg::JpegEncoder::new_with_quality(&mut out, 85).encode_image(&DynamicImage::ImageRgb8(picture.to_rgb8())).ok()?;
    if out.len() as u64 > MAX_IMAGE_BYTES {
        return None;
    }
    let stem = Path::new(name).file_stem().and_then(|s| s.to_str()).filter(|s| !s.is_empty()).unwrap_or("image");
    Some(Prepared { name: format!("{stem}.jpg"), media_type: "image/jpeg".into(), data: out.into() })
}

fn looks_like_image(name: &str) -> bool {
    let ext = Path::new(name).extension().and_then(|e| e.to_str()).unwrap_or_default().to_ascii_lowercase();
    matches!(ext.as_str(), "png" | "jpg" | "jpeg" | "gif" | "webp" | "bmp" | "tif" | "tiff" | "heic" | "heif" | "avif")
}

pub fn file(path: &Path) -> Result<Prepared, Problem> {
    let name = path.file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_else(|| "file".into());
    let meta = std::fs::metadata(path).map_err(|_| Problem::Unreadable(name.clone()))?;
    if meta.is_dir() {
        return Err(Problem::Folder);
    }
    if meta.len() > MAX_FILE_BYTES {
        return Err(Problem::TooLarge(name));
    }
    let data = std::fs::read(path).map_err(|_| Problem::Unreadable(name.clone()))?;
    if let Some(picture) = image(&name, &data) {
        return Ok(picture);
    }
    if looks_like_image(&name) {
        if let Some(picture) = system_image(path, &name) {
            return Ok(picture);
        }
    }
    if data.is_empty() {
        return Err(Problem::Unreadable(name));
    }
    Ok(Prepared { media_type: media_type(&name).into(), name, data: data.into() })
}

#[cfg(target_os = "macos")]
fn system_image(path: &Path, name: &str) -> Option<Prepared> {
    let dir = std::env::temp_dir().join(format!("agenthail-attach-{}-{}", std::process::id(), crate::catalog::request_key()));
    std::fs::create_dir_all(&dir).ok()?;
    let out = dir.join("image.jpg");
    let done = std::process::Command::new("/usr/bin/sips")
        .args(["-s", "format", "jpeg", "-s", "formatOptions", "85", "-Z", &MAX_IMAGE_EDGE.to_string()])
        .arg(path)
        .arg("--out")
        .arg(&out)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .is_ok_and(|s| s.success());
    let data = done.then(|| std::fs::read(&out).ok()).flatten();
    let _ = std::fs::remove_dir_all(&dir);
    image(name, &data?)
}

#[cfg(not(target_os = "macos"))]
fn system_image(_: &Path, _: &str) -> Option<Prepared> {
    None
}

pub fn picture(name: &str, png: &[u8]) -> Result<Prepared, Problem> {
    image(name, png).ok_or_else(|| Problem::Unreadable(name.to_string()))
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Paste {
    Files,
    Image,
    Text,
}

pub fn paste(files: usize, image: bool, text: &str) -> Paste {
    if files > 0 {
        Paste::Files
    } else if image && (text.trim().is_empty() || is_web_address(text)) {
        Paste::Image
    } else {
        Paste::Text
    }
}

fn is_web_address(text: &str) -> bool {
    let trimmed = text.trim();
    if trimmed.chars().any(char::is_whitespace) {
        return false;
    }
    url::Url::parse(trimmed).is_ok_and(|u| matches!(u.scheme(), "http" | "https" | "data"))
}

#[derive(Clone, Debug, PartialEq)]
pub enum State {
    Uploading,
    Ready(Upload),
    Failed(String),
}

#[derive(Clone, Debug, PartialEq)]
pub struct Chip {
    pub local: u64,
    pub name: String,
    pub image: bool,
    pub size: u64,
    pub data: Option<Arc<[u8]>>,
    pub state: State,
}

impl Chip {
    pub fn help(&self) -> String {
        match &self.state {
            State::Failed(error) => format!("{} didn't upload: {error}", self.name),
            State::Uploading => format!("{}, {}, uploading", self.name, size_label(self.size)),
            State::Ready(_) => format!("{}, {}", self.name, size_label(self.size)),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Hold {
    Uploading,
    Failed(String),
    TooMany,
}

impl Hold {
    pub fn note(&self) -> String {
        match self {
            Hold::Uploading => "Waiting for attachments to upload".into(),
            Hold::Failed(name) => format!("{name} can't be sent. Retry it, or remove it and attach it again."),
            Hold::TooMany => format!("A message can carry up to {MAX_COUNT} attachments. Remove some to send."),
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Tray {
    chips: HashMap<String, Vec<Chip>>,
    next: u64,
}

impl Tray {
    pub fn chips(&self, id: &str) -> &[Chip] {
        self.chips.get(id).map(Vec::as_slice).unwrap_or_default()
    }

    pub fn has(&self, id: &str) -> bool {
        !self.chips(id).is_empty()
    }

    pub fn add(&mut self, id: &str, file: Prepared) -> Result<Option<u64>, Problem> {
        let list = self.chips.entry(id.to_string()).or_default();
        if list.iter().any(|c| c.name == file.name && c.data.as_deref() == Some(&*file.data)) {
            return Ok(None);
        }
        if list.len() >= MAX_COUNT {
            return Err(Problem::TooMany);
        }
        self.next += 1;
        let image = file.is_image();
        list.push(Chip { local: self.next, name: file.name, image, size: file.data.len() as u64, data: Some(file.data), state: State::Uploading });
        Ok(Some(self.next))
    }

    pub fn remove(&mut self, local: u64) {
        for list in self.chips.values_mut() {
            list.retain(|c| c.local != local);
        }
        self.chips.retain(|_, list| !list.is_empty());
    }

    pub fn uploaded(&mut self, local: u64, result: Result<Upload, String>) {
        if let Some(chip) = self.chips.values_mut().flatten().find(|c| c.local == local) {
            chip.state = match result {
                Ok(upload) => State::Ready(upload),
                Err(error) => State::Failed(error),
            };
        }
    }

    pub fn retry(&mut self, local: u64) -> Option<(String, Arc<[u8]>)> {
        let chip = self.chips.values_mut().flatten().find(|c| c.local == local)?;
        let data = chip.data.clone().filter(|_| matches!(chip.state, State::Failed(_)))?;
        chip.state = State::Uploading;
        Some((chip.name.clone(), data))
    }

    pub fn ready(&self, id: &str) -> Result<Vec<Upload>, Hold> {
        if self.chips(id).len() > MAX_COUNT {
            return Err(Hold::TooMany);
        }
        let mut out = Vec::new();
        for chip in self.chips(id) {
            match &chip.state {
                State::Ready(upload) => out.push(upload.clone()),
                State::Uploading => return Err(Hold::Uploading),
                State::Failed(_) => return Err(Hold::Failed(chip.name.clone())),
            }
        }
        Ok(out)
    }

    pub fn clear(&mut self, id: &str) {
        self.chips.remove(id);
    }

    pub fn restore(&mut self, id: &str, uploads: &[Upload]) -> Vec<String> {
        let mut lost = Vec::new();
        for upload in uploads {
            let list = self.chips.entry(id.to_string()).or_default();
            if list.iter().any(|c| matches!(&c.state, State::Ready(u) if u.id == upload.id)) {
                continue;
            }
            if list.len() >= MAX_COUNT {
                lost.push(upload.name.clone());
                continue;
            }
            self.next += 1;
            list.push(Chip { local: self.next, name: upload.name.clone(), image: upload.is_image(), size: upload.bytes, data: None, state: State::Ready(upload.clone()) });
        }
        self.chips.retain(|_, list| !list.is_empty());
        lost
    }

    pub fn rekey(&mut self, rekeyed: &HashMap<String, String>) {
        let retired: Vec<String> = self.chips.keys().filter(|id| crate::composer::current(rekeyed, id) != id.as_str()).cloned().collect();
        for old in retired {
            let moving = self.chips.remove(&old).unwrap_or_default();
            let new = crate::composer::current(rekeyed, &old).to_string();
            self.chips.entry(new).or_default().extend(moving);
        }
    }

    pub fn expire(&mut self, id: &str, upload: &str) {
        for chip in self.chips.get_mut(id).into_iter().flatten() {
            if matches!(&chip.state, State::Ready(u) if u.id == upload) {
                chip.state = State::Failed("No longer on its computer. Remove it and attach it again.".into());
                chip.data = None;
            }
        }
    }

    pub fn to_json(&self) -> Value {
        Value::Object(
            self.chips
                .iter()
                .map(|(id, list)| {
                    let kept: Vec<Value> = list.iter().filter_map(|c| if let State::Ready(u) = &c.state { Some(u.to_json()) } else { None }).collect();
                    (id.clone(), Value::Array(kept))
                })
                .filter(|(_, v)| v.as_array().is_some_and(|a| !a.is_empty()))
                .collect(),
        )
    }

    pub fn from_json(value: &Value) -> Tray {
        let mut tray = Tray::default();
        for (id, raw) in value.as_object().into_iter().flatten() {
            let uploads: Vec<Upload> = raw.as_array().into_iter().flatten().filter_map(Upload::from_json).collect();
            tray.restore(id, &uploads);
        }
        tray
    }
}

pub fn missing_upload(error: &str) -> Option<String> {
    let rest = error.split_once("attachment \"")?.1;
    let (id, tail) = rest.split_once('"')?;
    tail.trim_start().starts_with("is not stored on this host").then(|| id.to_string())
}

pub fn media_type(name: &str) -> &'static str {
    let ext = Path::new(name).extension().and_then(|e| e.to_str()).unwrap_or_default().to_ascii_lowercase();
    match ext.as_str() {
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "pdf" => "application/pdf",
        "txt" | "log" => "text/plain",
        "md" | "markdown" => "text/markdown",
        "json" => "application/json",
        "csv" => "text/csv",
        "html" | "htm" => "text/html",
        "zip" => "application/zip",
        _ => "application/octet-stream",
    }
}

pub fn size_label(bytes: u64) -> String {
    let n = bytes as f64;
    match bytes {
        0..1000 => format!("{bytes} bytes"),
        1000..1_000_000 => format!("{} KB", (n / 1e3).round() as u64),
        1_000_000..1_000_000_000 => trimmed(n / 1e6, 1, "MB"),
        _ => trimmed(n / 1e9, 2, "GB"),
    }
}

fn trimmed(value: f64, places: usize, unit: &str) -> String {
    let text = format!("{value:.places$}");
    let text = if text.contains('.') { text.trim_end_matches('0').trim_end_matches('.').to_string() } else { text };
    format!("{text} {unit}")
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum FileKind {
    Pdf,
    Image,
    Movie,
    Audio,
    Archive,
    Text,
    Other,
}

fn extension(name: &str) -> String {
    Path::new(name).extension().and_then(|e| e.to_str()).unwrap_or_default().to_ascii_lowercase()
}

pub fn file_kind(name: &str, media: &str) -> FileKind {
    let ext = extension(name);
    let media = media.split(';').next().unwrap_or_default().trim();
    if media == "application/pdf" || ext == "pdf" {
        FileKind::Pdf
    } else if media.starts_with("image/") || matches!(ext.as_str(), "png" | "jpg" | "jpeg" | "gif" | "webp" | "heic" | "bmp" | "tiff" | "svg") {
        FileKind::Image
    } else if media.starts_with("video/") || matches!(ext.as_str(), "mov" | "mp4" | "m4v" | "webm" | "mkv") {
        FileKind::Movie
    } else if media.starts_with("audio/") || matches!(ext.as_str(), "mp3" | "m4a" | "wav" | "aac" | "flac" | "ogg") {
        FileKind::Audio
    } else if matches!(ext.as_str(), "zip" | "gz" | "tgz" | "tar" | "bz2" | "xz" | "7z" | "rar") || media == "application/zip" {
        FileKind::Archive
    } else if media.starts_with("text/") || media == "application/json" || matches!(ext.as_str(), "txt" | "md" | "json" | "csv" | "log" | "rs" | "go" | "swift" | "py" | "js" | "ts" | "sh" | "yml" | "yaml" | "toml" | "html" | "css") {
        FileKind::Text
    } else {
        FileKind::Other
    }
}

pub fn kind_label(name: &str, media: &str) -> Option<String> {
    let ext = extension(name);
    let label = match (file_kind(name, media), ext.as_str()) {
        (FileKind::Pdf, _) => "PDF document".to_string(),
        (_, "png" | "jpg" | "jpeg" | "gif" | "webp" | "heic" | "bmp" | "tiff") => format!("{} image", if ext == "jpg" { "JPEG".into() } else { ext.to_uppercase() }),
        (_, "txt" | "log") => "Plain text".into(),
        (_, "md" | "markdown") => "Markdown".into(),
        (_, "json") => "JSON".into(),
        (_, "csv") => "CSV".into(),
        (_, "html" | "htm") => "HTML".into(),
        (FileKind::Archive, _) => format!("{} archive", ext.to_uppercase()),
        (FileKind::Movie, _) => "Movie".into(),
        (FileKind::Audio, _) => "Audio".into(),
        (_, "") => return None,
        _ => format!("{} file", ext.to_uppercase()),
    };
    Some(label)
}

pub fn sent_name(item: &Item) -> String {
    item.attachment.as_ref().and_then(|a| a.name.clone()).filter(|n| !n.is_empty()).or_else(|| item.files.first().map(|f| f.name.clone())).unwrap_or_else(|| item.title.clone())
}

pub fn sent_detail(item: &Item) -> String {
    let name = sent_name(item);
    let media = item.attachment.as_ref().map(|a| a.media_type.as_str()).unwrap_or_default();
    let bytes = item.attachment.as_ref().map(|a| a.bytes).filter(|b| *b > 0).or_else(|| item.files.first().and_then(|f| f.bytes));
    [kind_label(&name, media), bytes.map(size_label)].into_iter().flatten().collect::<Vec<_>>().join(" · ")
}

pub fn sent_reason(item: &Item, host: Option<&str>) -> Option<String> {
    if item.attachment.is_some() {
        return None;
    }
    let host = host.filter(|h| !h.is_empty()).unwrap_or("its computer");
    let Some(bytes) = item.files.first().and_then(|f| f.bytes) else { return Some(format!("No longer on {host}.")) };
    if bytes > OPEN_LIMIT {
        return Some(format!("Too large to open here, {}.", size_label(bytes)));
    }
    Some("This file can't be opened here.".into())
}

pub fn safe_name(name: &str, fallback: &str) -> String {
    let kept: String = name.chars().filter(|c| !c.is_control() && !matches!(c, '/' | '\\' | ':')).collect();
    let text = kept.trim().trim_start_matches('.').trim().to_string();
    if text.is_empty() {
        return fallback.to_string();
    }
    if text.chars().count() <= 120 {
        return text;
    }
    let path = Path::new(&text);
    match (path.file_stem().and_then(|s| s.to_str()), path.extension().and_then(|e| e.to_str())) {
        (Some(stem), Some(ext)) if !ext.is_empty() && ext.chars().count() <= 16 => {
            format!("{}.{ext}", stem.chars().take(120 - ext.chars().count() - 1).collect::<String>())
        }
        _ => text.chars().take(120).collect(),
    }
}

pub fn folder(id: &str) -> String {
    let text: String = id.chars().filter(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_')).take(120).collect();
    if text.is_empty() { "attachment".into() } else { text }
}

#[cfg(test)]
mod tests {
    use super::*;
    use image::{ImageEncoder, RgbImage};

    fn png(width: u32, height: u32) -> Vec<u8> {
        let mut out = Vec::new();
        image::codecs::png::PngEncoder::new(&mut out).write_image(RgbImage::new(width, height).as_raw(), width, height, image::ExtendedColorType::Rgb8).unwrap();
        out
    }

    #[test]
    fn small_png_goes_as_it_is() {
        let data = png(40, 20);
        let prepared = image("shot.png", &data).unwrap();
        assert_eq!(prepared.media_type, "image/png");
        assert_eq!(prepared.name, "shot.png");
        assert_eq!(&*prepared.data, &data[..]);
    }

    #[test]
    fn wide_image_becomes_a_jpeg_within_the_edge() {
        let prepared = image("wide.png", &png(4000, 1000)).unwrap();
        assert_eq!((prepared.name.as_str(), prepared.media_type.as_str()), ("wide.jpg", "image/jpeg"));
        let decoded = image::load_from_memory(&prepared.data).unwrap();
        assert_eq!((decoded.width(), decoded.height()), (2048, 512));
    }

    #[test]
    fn other_image_formats_become_jpeg() {
        let mut bmp = Vec::new();
        image::codecs::bmp::BmpEncoder::new(&mut bmp).write_image(RgbImage::new(10, 10).as_raw(), 10, 10, image::ExtendedColorType::Rgb8).unwrap();
        let prepared = image("pic.bmp", &bmp).unwrap();
        assert_eq!((prepared.name.as_str(), prepared.media_type.as_str()), ("pic.jpg", "image/jpeg"));
    }

    #[test]
    fn non_images_are_not_pictures() {
        assert!(image("notes.txt", b"hello there").is_none());
    }

    #[test]
    fn files_from_paths() {
        let dir = std::env::temp_dir().join(format!("feed-attach-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let text = dir.join("notes.md");
        std::fs::write(&text, "# hi").unwrap();
        let prepared = file(&text).unwrap();
        assert_eq!((prepared.name.as_str(), prepared.media_type.as_str(), &*prepared.data), ("notes.md", "text/markdown", &b"# hi"[..]));
        assert!(!prepared.is_image());
        let shot = dir.join("shot.png");
        std::fs::write(&shot, png(8, 8)).unwrap();
        assert!(file(&shot).unwrap().is_image());
        assert_eq!(file(&dir), Err(Problem::Folder));
        let empty = dir.join("empty.txt");
        std::fs::write(&empty, "").unwrap();
        assert_eq!(file(&empty), Err(Problem::Unreadable("empty.txt".into())));
        let big = dir.join("big.bin");
        std::fs::File::create(&big).unwrap().set_len(MAX_FILE_BYTES + 1).unwrap();
        assert_eq!(file(&big), Err(Problem::TooLarge("big.bin".into())));
        assert_eq!(file(&dir.join("missing.txt")), Err(Problem::Unreadable("missing.txt".into())));
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn paste_takes_files_then_images_then_text() {
        assert_eq!(paste(1, true, "words"), Paste::Files);
        assert_eq!(paste(0, true, ""), Paste::Image);
        assert_eq!(paste(0, true, "https://example.com/cat.png"), Paste::Image);
        assert_eq!(paste(0, true, "a\tb\nc\td"), Paste::Text);
        assert_eq!(paste(0, false, ""), Paste::Text);
    }

    fn prepared(name: &str, data: &[u8]) -> Prepared {
        Prepared { name: name.into(), media_type: "text/plain".into(), data: data.into() }
    }

    fn upload(id: &str) -> Upload {
        Upload { id: id.into(), name: format!("{id}.txt"), media_type: "text/plain".into(), bytes: 3, ..Default::default() }
    }

    #[test]
    fn tray_holds_sends_until_uploads_finish() {
        let mut tray = Tray::default();
        let a = tray.add("s1", prepared("a.txt", b"aaa")).unwrap().unwrap();
        let b = tray.add("s1", prepared("b.txt", b"bbb")).unwrap().unwrap();
        assert_eq!(tray.ready("s1"), Err(Hold::Uploading));
        tray.uploaded(a, Ok(upload("a")));
        tray.uploaded(b, Err("offline".into()));
        assert_eq!(tray.ready("s1"), Err(Hold::Failed("b.txt".into())));
        assert_eq!(tray.retry(b).map(|(n, d)| (n, d.to_vec())), Some(("b.txt".to_string(), b"bbb".to_vec())));
        assert_eq!(tray.retry(b), None);
        tray.uploaded(b, Ok(upload("b")));
        assert_eq!(tray.ready("s1").unwrap().iter().map(|u| u.id.as_str()).collect::<Vec<_>>(), ["a", "b"]);
        tray.remove(a);
        assert_eq!(tray.ready("s1").unwrap().len(), 1);
        assert_eq!(tray.ready("other"), Ok(vec![]));
    }

    #[test]
    fn tray_refuses_a_ninth_and_skips_repeats() {
        let mut tray = Tray::default();
        assert!(tray.add("s1", prepared("a.txt", b"0")).unwrap().is_some());
        assert_eq!(tray.add("s1", prepared("a.txt", b"0")), Ok(None));
        for i in 1..MAX_COUNT {
            tray.add("s1", prepared(&format!("{i}.txt"), &[i as u8])).unwrap();
        }
        assert_eq!(tray.add("s1", prepared("9.txt", b"9")), Err(Problem::TooMany));
        assert_eq!(tray.chips("s1").len(), MAX_COUNT);
    }

    #[test]
    fn tray_keeps_uploaded_chips_across_restarts_and_rekeys() {
        let mut tray = Tray::default();
        let a = tray.add("s1", prepared("a.txt", b"aaa")).unwrap().unwrap();
        tray.add("s1", prepared("b.txt", b"bbb")).unwrap();
        tray.uploaded(a, Ok(upload("a")));
        let back = Tray::from_json(&tray.to_json());
        assert_eq!(back.ready("s1").unwrap(), vec![upload("a")]);
        let mut back = back;
        back.rekey(&HashMap::from([("s1".to_string(), "s2".to_string())]));
        assert!(!back.has("s1"));
        assert_eq!(back.ready("s2").unwrap(), vec![upload("a")]);
        assert_eq!(back.restore("s2", &[upload("a")]), Vec::<String>::new());
        assert_eq!(back.chips("s2").len(), 1);
    }

    #[test]
    fn a_merge_past_the_limit_keeps_every_chip_and_holds_the_send() {
        let mut tray = Tray::default();
        for (session, n) in [("old", 5), ("new", 5)] {
            let uploads: Vec<Upload> = (0..n).map(|i| upload(&format!("{session}{i}"))).collect();
            tray.restore(session, &uploads);
        }
        tray.rekey(&HashMap::from([("old".to_string(), "new".to_string())]));
        assert_eq!(tray.chips("new").len(), 10);
        assert_eq!(tray.ready("new"), Err(Hold::TooMany));
        for chip in tray.chips("new")[..2].to_vec() {
            tray.remove(chip.local);
        }
        assert_eq!(tray.ready("new").unwrap().len(), 8);
    }

    #[test]
    fn an_expired_upload_becomes_a_chip_to_attach_again() {
        let error = r#"attachment "upload:abc/shot.png" is not stored on this host; upload it again"#;
        assert_eq!(missing_upload(error).as_deref(), Some("upload:abc/shot.png"));
        assert_eq!(missing_upload("message is required"), None);
        let mut tray = Tray::default();
        tray.restore("s1", &[upload("upload:abc/shot.png"), upload("b")]);
        tray.expire("s1", "upload:abc/shot.png");
        assert_eq!(tray.ready("s1"), Err(Hold::Failed("upload:abc/shot.png.txt".into())));
        let gone = tray.chips("s1")[0].local;
        assert_eq!(tray.retry(gone), None);
        tray.remove(gone);
        assert_eq!(tray.ready("s1").unwrap(), vec![upload("b")]);
    }

    #[test]
    fn restore_names_what_no_longer_fits() {
        let mut tray = Tray::default();
        let many: Vec<Upload> = (0..MAX_COUNT + 1).map(|i| upload(&i.to_string())).collect();
        assert_eq!(tray.restore("s1", &many), vec![format!("{MAX_COUNT}.txt")]);
    }

    #[test]
    fn sizes_read_like_the_finder() {
        assert_eq!(size_label(12), "12 bytes");
        assert_eq!(size_label(340_400), "340 KB");
        assert_eq!(size_label(1_260_000), "1.3 MB");
        assert_eq!(size_label(2_000_000), "2 MB");
        assert_eq!(size_label(1_500_000_000), "1.5 GB");
    }

    fn sent(files: Vec<crate::item::AttachedFile>, attachment: Option<crate::item::Attachment>) -> Item {
        Item { id: "f1".into(), kind: "file".into(), title: "report.pdf".into(), files, attachment, ..Default::default() }
    }

    #[test]
    fn sent_files_say_what_they_are_and_why_they_cant_open() {
        let on_disk = crate::item::AttachedFile { name: "report.pdf".into(), path: "/w/report.pdf".into(), bytes: Some(2_000_000) };
        let attachment = crate::item::Attachment { id: "a1".into(), media_type: "application/pdf".into(), bytes: 2_000_000, name: Some("report.pdf".into()), ..Default::default() };
        let ready = sent(vec![on_disk.clone()], Some(attachment));
        assert_eq!(sent_name(&ready), "report.pdf");
        assert_eq!(sent_detail(&ready), "PDF document · 2 MB");
        assert_eq!(sent_reason(&ready, Some("forge")), None);
        assert_eq!(sent_reason(&sent(vec![crate::item::AttachedFile { bytes: None, ..on_disk.clone() }], None), Some("forge")).as_deref(), Some("No longer on forge."));
        assert_eq!(sent_reason(&sent(vec![crate::item::AttachedFile { bytes: None, ..on_disk.clone() }], None), None).as_deref(), Some("No longer on its computer."));
        assert_eq!(sent_reason(&sent(vec![crate::item::AttachedFile { bytes: Some(OPEN_LIMIT + 1), ..on_disk.clone() }], None), None).as_deref(), Some("Too large to open here, 52.4 MB."));
        assert_eq!(sent_reason(&sent(vec![on_disk], None), None).as_deref(), Some("This file can't be opened here."));
    }

    struct Heard {
        line: String,
        content_type: String,
        body: Vec<u8>,
    }

    fn host(status: u16, reply: Vec<u8>) -> (crate::catalog::Client, std::sync::mpsc::Receiver<Heard>) {
        use std::io::{BufRead, BufReader, Read, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let (stream, _) = listener.accept().unwrap();
            let mut reader = BufReader::new(stream.try_clone().unwrap());
            let mut line = String::new();
            reader.read_line(&mut line).unwrap();
            let (mut content_type, mut length) = (String::new(), 0usize);
            loop {
                let mut header = String::new();
                reader.read_line(&mut header).unwrap();
                let header = header.trim_end();
                if header.is_empty() {
                    break;
                }
                let (name, value) = header.split_once(':').unwrap();
                match name.to_ascii_lowercase().as_str() {
                    "content-type" => content_type = value.trim().into(),
                    "content-length" => length = value.trim().parse().unwrap(),
                    _ => {}
                }
            }
            let mut body = vec![0; length];
            reader.read_exact(&mut body).unwrap();
            tx.send(Heard { line: line.trim_end().into(), content_type, body }).unwrap();
            let mut stream = stream;
            write!(stream, "HTTP/1.1 {status} X\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", reply.len()).unwrap();
            stream.write_all(&reply).unwrap();
        });
        (crate::catalog::Client::new(&base, "secret"), rx)
    }

    #[test]
    fn uploads_post_the_bytes_for_the_session() {
        let (client, heard) = host(200, br#"{"upload":{"id":"up/abc/shot.png","name":"shot.png","mediaType":"image/png","bytes":3,"width":2,"height":1}}"#.to_vec());
        let upload = client.upload("s 1", "my shot.png", b"png").unwrap();
        assert_eq!(upload, Upload { id: "up/abc/shot.png".into(), name: "shot.png".into(), media_type: "image/png".into(), bytes: 3, width: 2, height: 1 });
        let got = heard.recv().unwrap();
        assert_eq!(got.line, "POST /api/v1/uploads?sessionId=s+1&name=my+shot.png HTTP/1.1");
        assert_eq!((got.content_type.as_str(), got.body.as_slice()), ("application/octet-stream", &b"png"[..]));
    }

    #[test]
    fn upload_refusals_say_why() {
        let (client, _heard) = host(413, br#"{"error":{"code":"upload_too_large","message":"Images can be up to 5 MiB and other files up to 25 MiB."}}"#.to_vec());
        let error = client.upload("s1", "big.png", b"x").unwrap_err();
        assert!(!error.maybe_delivered, "{error:?}");
        assert!(error.message.contains("5 MiB"), "{}", error.message);
    }

    #[test]
    fn sent_attachments_come_back_as_bytes() {
        let (client, heard) = host(200, vec![0, 1, 2, 255]);
        assert_eq!(client.attachment("s1", "att/1").unwrap(), vec![0, 1, 2, 255]);
        assert_eq!(heard.recv().unwrap().line, "GET /api/v1/session-attachment?sessionId=s1&id=att%2F1 HTTP/1.1");
        let (client, _heard) = host(404, br#"{"error":{"code":"attachment_not_found","message":"The referenced attachment was not found."}}"#.to_vec());
        assert!(client.attachment("s1", "gone").unwrap_err().contains("not found"));
    }

    #[test]
    fn cache_names_stay_inside_their_folder() {
        assert_eq!(safe_name("../../etc/passwd", "file"), "etcpasswd");
        assert_eq!(safe_name(" .hidden ", "file"), "hidden");
        assert_eq!(safe_name("\u{7}", "file"), "file");
        let long = format!("{}.pdf", "a".repeat(200));
        let kept = safe_name(&long, "file");
        assert_eq!(kept.chars().count(), 120);
        assert!(kept.ends_with(".pdf"));
        assert_eq!(folder("att:1/../x"), "att1x");
        assert_eq!(folder("///"), "attachment");
    }
}
