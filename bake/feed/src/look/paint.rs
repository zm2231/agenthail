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

pub fn pane_alpha(behind: f32, tint: f32, boundary: f32, dark: bool, minimum: f32) -> f32 {
    let short = if dark { behind > boundary } else { behind < boundary };
    if !short || (behind - tint).abs() < 1e-6 {
        return minimum;
    }
    let need = (behind - boundary) / (behind - tint);
    need.clamp(0.0, 1.0).max(minimum)
}

pub fn worst(rgba: &[u8], width: u32, dark: bool, cell: usize) -> Rgb {
    let w = width.max(1) as usize;
    let h = rgba.len() / 4 / w;
    if h == 0 {
        return if dark { [1.0; 3] } else { [0.0; 3] };
    }
    let cell = cell.max(1);
    let (cw, ch) = (w.div_ceil(cell), h.div_ceil(cell));
    let mut sums = vec![([0.0f32; 3], 0u32); cw * ch];
    for y in 0..h {
        for x in 0..w {
            let i = (y * w + x) * 4;
            let slot = &mut sums[(y / cell) * cw + x / cell];
            for k in 0..3 {
                slot.0[k] += rgba[i + k] as f32 / 255.0;
            }
            slot.1 += 1;
        }
    }
    let cells = sums.into_iter().filter(|(_, n)| *n > 0).map(|(c, n)| c.map(|v| v / n as f32));
    let key = |c: &Rgb| luminance(*c);
    let pick = if dark { cells.max_by(|a, b| key(a).total_cmp(&key(b))) } else { cells.min_by(|a, b| key(a).total_cmp(&key(b))) };
    pick.unwrap_or(if dark { [1.0; 3] } else { [0.0; 3] })
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
}

pub fn decode(data: &[u8], max_side: u32, sample_side: u32) -> Result<Decoded, String> {
    let picture = image::load_from_memory(data).map_err(|e| e.to_string())?;
    let original = (picture.width(), picture.height());
    let full = picture.to_rgba8();
    let (fw, fh) = (full.width(), full.height());
    let full = full.into_raw();
    let brightest = extreme(&full, fw, fh, sample_side, true);
    let darkest = extreme(&full, fw, fh, sample_side, false);
    let shown = if fw.max(fh) > max_side {
        let small = picture.resize(max_side, max_side, image::imageops::FilterType::Triangle).to_rgba8();
        (small.width(), small.height(), small.into_raw())
    } else {
        (fw, fh, full)
    };
    Ok(Decoded { original, shown, brightest, darkest })
}

pub fn thumbnail(data: &[u8], side: u32) -> Result<(u32, u32, Vec<u8>), String> {
    let small = image::load_from_memory(data).map_err(|e| e.to_string())?.thumbnail(side, side).to_rgba8();
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
    let out = best.into_iter().flat_map(|b| {
        let [r, g, bl] = b.map(|(_, c)| c).unwrap_or([0; 3]);
        [r, g, bl, 255]
    });
    (cw as u32, ch as u32, out.collect())
}
