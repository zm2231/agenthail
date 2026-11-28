use super::{Fit, Position, Rgb};

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Placement {
    pub rect: [f32; 4],
    pub tile: bool,
}

pub fn place(fit: Fit, position: Position, image: (f32, f32), area: (f32, f32)) -> Placement {
    let (iw, ih) = (image.0.max(1.0), image.1.max(1.0));
    if fit == Fit::Tile {
        return Placement { rect: [0.0, 0.0, iw, ih], tile: true };
    }
    let scale = match fit {
        Fit::Contain => (area.0 / iw).min(area.1 / ih),
        Fit::Center => 1.0,
        _ => (area.0 / iw).max(area.1 / ih),
    };
    let (w, h) = (iw * scale, ih * scale);
    let ax = match position {
        Position::Left => 0.0,
        Position::Right => 1.0,
        _ => 0.5,
    };
    let ay = match position {
        Position::Top => 0.0,
        Position::Bottom => 1.0,
        _ => 0.5,
    };
    Placement { rect: [(area.0 - w) * ax, (area.1 - h) * ay, w, h], tile: false }
}

pub fn linear(c: f32) -> f32 {
    if c <= 0.04045 { c / 12.92 } else { ((c + 0.055) / 1.055).powf(2.4) }
}

pub fn gamma(c: f32) -> f32 {
    if c <= 0.0031308 { c * 12.92 } else { 1.055 * c.powf(1.0 / 2.4) - 0.055 }
}

pub fn luminance(c: Rgb) -> f32 {
    0.2126 * linear(c[0]) + 0.7152 * linear(c[1]) + 0.0722 * linear(c[2])
}

pub fn contrast(a: f32, b: f32) -> f32 {
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

pub fn boundary(roles: &[f32], dark: bool, target: f32) -> f32 {
    if dark {
        roles.iter().map(|l| (l + 0.05) / target - 0.05).fold(1.0, f32::min).max(0.0)
    } else {
        roles.iter().map(|l| target * (l + 0.05) - 0.05).fold(0.0, f32::max).min(1.0)
    }
}

pub fn pane_alpha(behind: Rgb, tint: Rgb, boundary: f32, dark: bool, minimum: f32) -> f32 {
    let fits = |a: f32| {
        let l = luminance(blend(behind, tint, a));
        if dark { l <= boundary } else { l >= boundary }
    };
    if fits(minimum) || !fits(1.0) {
        return if fits(minimum) { minimum } else { 1.0 };
    }
    let (mut lo, mut hi) = (minimum, 1.0f32);
    for _ in 0..24 {
        let mid = (lo + hi) / 2.0;
        if fits(mid) { hi = mid } else { lo = mid }
    }
    hi
}

pub fn worst(rgba: &[u8], width: u32, dark: bool, cell: usize) -> Rgb {
    let w = width.max(1) as usize;
    let h = rgba.len() / 4 / w;
    if h == 0 {
        return if dark { [1.0; 3] } else { [0.0; 3] };
    }
    let size = cell.max(1).min(w).min(h);
    let stride = w + 1;
    let mut table = vec![[0.0f64; 3]; stride * (h + 1)];
    for y in 0..h {
        let mut row = [0.0f64; 3];
        for x in 0..w {
            let i = (y * w + x) * 4;
            for k in 0..3 {
                row[k] += rgba[i + k] as f64 / 255.0;
                table[(y + 1) * stride + x + 1][k] = table[y * stride + x + 1][k] + row[k];
            }
        }
    }
    let area = (size * size) as f64;
    let mut best: Option<(f32, Rgb)> = None;
    for y in 0..=h - size {
        for x in 0..=w - size {
            let (a, b, c, d) = (table[y * stride + x], table[y * stride + x + size], table[(y + size) * stride + x], table[(y + size) * stride + x + size]);
            let mean = [0, 1, 2].map(|k| ((d[k] - b[k] - c[k] + a[k]) / area) as f32);
            let l = luminance(mean);
            let better = match best {
                None => true,
                Some((b, _)) => if dark { l > b } else { l < b },
            };
            if better {
                best = Some((l, mean));
            }
        }
    }
    best.map(|(_, c)| c).unwrap_or(if dark { [1.0; 3] } else { [0.0; 3] })
}

pub fn readable_text(color: Rgb, surfaces: &[Rgb], dark: bool, target: f32) -> Rgb {
    let toward = if dark { [1.0; 3] } else { [0.0; 3] };
    let fits = |c: Rgb| surfaces.iter().all(|s| contrast(luminance(c), luminance(*s)) >= target);
    (0..=20).map(|i| blend(color, toward, i as f32 / 20.0)).find(|c| fits(*c)).unwrap_or(toward)
}

pub fn reachable(tint: Rgb, boundary: f32, dark: bool) -> Rgb {
    let toward = if dark { [0.0; 3] } else { [1.0; 3] };
    let fits = |c: Rgb| if dark { luminance(c) <= boundary } else { luminance(c) >= boundary };
    (0..=20).map(|i| blend(tint, toward, i as f32 / 20.0)).find(|c| fits(*c)).unwrap_or(toward)
}

pub fn blend(under: Rgb, over: Rgb, alpha: f32) -> Rgb {
    [0, 1, 2].map(|i| under[i] * (1.0 - alpha) + over[i] * alpha)
}

pub fn mesh(columns: usize, rows: usize, colors: &[Rgb], aspect: f32) -> (u32, u32, Vec<u8>) {
    let mw = 192usize;
    let mh = ((192.0 / aspect.max(0.1)) as usize).clamp(2, 1920);
    let mut out = vec![255u8; mw * mh * 4];
    if columns < 2 || rows < 2 || colors.len() != columns * rows {
        return (mw as u32, mh as u32, out);
    }
    let smooth = |t: f32| t * t * (3.0 - 2.0 * t);
    let at = |c: usize, r: usize| colors[r * columns + c].map(linear);
    for y in 0..mh {
        let fy = y as f32 / (mh - 1) as f32 * (rows - 1) as f32;
        let r0 = (fy as usize).min(rows - 2);
        let ty = smooth(fy - r0 as f32);
        for x in 0..mw {
            let fx = x as f32 / (mw - 1) as f32 * (columns - 1) as f32;
            let c0 = (fx as usize).min(columns - 2);
            let tx = smooth(fx - c0 as f32);
            let top = blend(at(c0, r0), at(c0 + 1, r0), tx);
            let bottom = blend(at(c0, r0 + 1), at(c0 + 1, r0 + 1), tx);
            let v = blend(top, bottom, ty);
            let i = (y * mw + x) * 4;
            for k in 0..3 {
                out[i + k] = (gamma(v[k]).clamp(0.0, 1.0) * 255.0).round() as u8;
            }
        }
    }
    (mw as u32, mh as u32, out)
}

pub struct Decoded {
    pub original: (u32, u32),
    pub shown: (u32, u32, Vec<u8>),
    pub brightest: (u32, u32, Vec<u8>),
    pub darkest: (u32, u32, Vec<u8>),
    pub see_through: bool,
}

fn is_heic(data: &[u8]) -> bool {
    data.len() > 12 && &data[4..8] == b"ftyp" && matches!(&data[8..12], b"heic" | b"heix" | b"heim" | b"heis" | b"mif1" | b"msf1")
}

fn load(data: &[u8]) -> Result<image::DynamicImage, String> {
    if is_heic(data) {
        return from_heic(data);
    }
    let mut decoder = image::ImageReader::new(std::io::Cursor::new(data)).with_guessed_format().map_err(|e| e.to_string())?.into_decoder().map_err(|e| e.to_string())?;
    let orientation = image::ImageDecoder::orientation(&mut decoder).map_err(|e| e.to_string())?;
    let mut picture = image::DynamicImage::from_decoder(decoder).map_err(|e| e.to_string())?;
    picture.apply_orientation(orientation);
    Ok(picture)
}

#[cfg(target_os = "macos")]
fn from_heic(data: &[u8]) -> Result<image::DynamicImage, String> {
    let dir = std::env::temp_dir().join(format!("agenthail-heic-{}-{}", std::process::id(), std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0)));
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let (input, output) = (dir.join("in.heic"), dir.join("out.png"));
    let converted = std::fs::write(&input, data)
        .map_err(|e| e.to_string())
        .and_then(|_| std::process::Command::new("/usr/bin/sips").args(["-s", "format", "png"]).arg(&input).arg("--out").arg(&output).output().map_err(|e| e.to_string()))
        .and_then(|o| if o.status.success() { std::fs::read(&output).map_err(|e| e.to_string()) } else { Err(String::from_utf8_lossy(&o.stderr).trim().to_string()) });
    let _ = std::fs::remove_file(&input);
    let _ = std::fs::remove_file(&output);
    let _ = std::fs::remove_dir(&dir);
    image::load_from_memory(&converted?).map_err(|e| e.to_string())
}

#[cfg(not(target_os = "macos"))]
fn from_heic(_: &[u8]) -> Result<image::DynamicImage, String> {
    Err("HEIC pictures can be shown only on a Mac".into())
}

pub fn decode(data: &[u8], max_side: u32, sample_side: u32) -> Result<Decoded, String> {
    let picture = load(data)?;
    let original = (picture.width(), picture.height());
    let full = picture.to_rgba8();
    let (fw, fh) = (full.width(), full.height());
    let full = full.into_raw();
    let see_through = full.chunks_exact(4).any(|p| p[3] < 255);
    let brightest = extreme(&full, fw, fh, sample_side, true);
    let darkest = extreme(&full, fw, fh, sample_side, false);
    let shown = if fw.max(fh) > max_side {
        let small = picture.resize(max_side, max_side, image::imageops::FilterType::Triangle).to_rgba8();
        (small.width(), small.height(), small.into_raw())
    } else {
        (fw, fh, full)
    };
    Ok(Decoded { original, shown, brightest, darkest, see_through })
}

pub fn thumbnail(data: &[u8], side: u32) -> Result<(u32, u32, Vec<u8>), String> {
    let small = load(data)?.thumbnail(side, side).to_rgba8();
    Ok((small.width(), small.height(), small.into_raw()))
}

pub fn extreme(rgba: &[u8], w: u32, h: u32, side: u32, bright: bool) -> (u32, u32, Vec<u8>) {
    let cell = w.max(h).div_ceil(side.max(1)).max(1) as usize;
    let (w, h) = (w as usize, h as usize);
    let (cw, ch) = (w.div_ceil(cell), h.div_ceil(cell));
    let mut best: Vec<Option<(f32, [u8; 3])>> = vec![None; cw * ch];
    for y in 0..h {
        for x in 0..w {
            let i = (y * w + x) * 4;
            if rgba[i + 3] == 0 {
                continue;
            }
            let c = [rgba[i], rgba[i + 1], rgba[i + 2]];
            let l = luminance(c.map(|v| v as f32 / 255.0));
            let slot = &mut best[(y / cell) * cw + x / cell];
            let better = match slot {
                None => true,
                Some((b, _)) => if bright { l > *b } else { l < *b },
            };
            if better {
                *slot = Some((l, c));
            }
        }
    }
    let out = best.into_iter().flat_map(|b| match b {
        Some((_, [r, g, bl])) => [r, g, bl, 255],
        None => [0; 4],
    });
    (cw as u32, ch as u32, out.collect())
}
