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

pub fn worst(rgba: &[u8], dark: bool) -> Rgb {
    let mut pixels: Vec<(f32, Rgb)> = rgba
        .chunks_exact(4)
        .map(|p| {
            let c = [p[0] as f32 / 255.0, p[1] as f32 / 255.0, p[2] as f32 / 255.0];
            (luminance(c), c)
        })
        .collect();
    if pixels.is_empty() {
        return if dark { [1.0; 3] } else { [0.0; 3] };
    }
    pixels.sort_by(|a, b| a.0.total_cmp(&b.0));
    let at = if dark { (pixels.len() - 1) * 95 / 100 } else { (pixels.len() - 1) * 5 / 100 };
    pixels[at].1
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

pub fn decode(data: &[u8], max_side: u32) -> Result<(u32, u32, Vec<u8>), String> {
    let picture = image::load_from_memory(data).map_err(|e| e.to_string())?;
    let picture = if picture.width().max(picture.height()) > max_side { picture.resize(max_side, max_side, image::imageops::FilterType::Triangle) } else { picture };
    let rgba = picture.to_rgba8();
    Ok((rgba.width(), rgba.height(), rgba.into_raw()))
}
