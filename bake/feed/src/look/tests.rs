use serde_json::json;

use super::*;
use crate::catalog::parse_time;

const LIVE: &str = r##"{"rev":"a070bfe77ead","text":"version = 1\nbackground = art: field\n","values":{"background":"image: art/field-dark.jpg","background.dim":0,"background.fit":"cover","background.light":"image: art/field-light.jpg","background.opacity":1,"background.position":"center","background.session-opacity":1,"glass":"on","glass.blur":0,"glass.border":0.08,"glass.composer.opacity":0.3,"glass.gutter":0,"glass.opacity":0,"glass.readability":"auto","glass.tint":"ground","scrim.tint":"auto","text.scale":1,"texture":"none","texture.spacing":3,"texture.strength":0.2,"theme":"system","theme.accent":"system","contrast.target":4.5},"colors":{"accent":{"systemAccent":true},"body":{"dark":"#ececee","light":"#1d1d1f"},"muted":{"dark":"#a3a3a8","light":"#55565c"}},"chosen":{"background":"art: field","glass.blur":"0"},"overrides":{"glass.blur":"mac"},"problems":[{"line":7,"key":"glass.sidebar.opacity","message":"glass.sidebar.opacity was removed"}]}"##;

#[test]
fn reads_the_daemon_view() {
    let view: View = serde_json::from_str(LIVE).unwrap();
    let look = Look::from_view(&view);
    assert_eq!(look.rev, "a070bfe77ead");
    assert_eq!(look.theme, Theme::System);
    assert_eq!(look.background, Wallpaper::Image("art/field-dark.jpg".into()));
    assert_eq!(look.background_light, Wallpaper::Image("art/field-light.jpg".into()));
    assert_eq!(look.images(), vec!["art/field-dark.jpg".to_string(), "art/field-light.jpg".to_string()]);
    assert_eq!((look.fit, look.position), (Fit::Cover, Position::Center));
    assert_eq!((look.blur, look.pane_opacity, look.float_opacity), (0.0, 0.0, 0.3));
    assert!(look.glass && look.readable);
    assert_eq!(look.accent, None);
    assert_eq!(look.role("body", true), Some([0xec as f32 / 255.0, 0xec as f32 / 255.0, 0xee as f32 / 255.0]));
    assert_eq!(look.role("muted", false), hex("#55565c"));
    assert!(look.translucent(true));
    assert!(!look.translucent(false));
    assert_eq!(view.overrides.get("glass.blur").map(String::as_str), Some("mac"));
    assert_eq!(view.problems[0].line, 7);
}

#[test]
fn defaults_follow_the_schema_without_a_daemon() {
    let look = Look::default();
    assert_eq!(look.background, Wallpaper::None);
    assert_eq!((look.blur, look.pane_opacity, look.float_opacity, look.text_scale, look.target), (30.0, 0.3, 0.5, 1.0, 4.5));
    assert!(!look.translucent(true));
}

#[test]
fn out_of_range_numbers_are_held_to_the_schema() {
    let view = View { values: serde_json::from_value(json!({"text.scale": 3, "glass.blur": -2, "background.dim": 0.95, "theme": "light"})).unwrap(), ..View::default() };
    let look = Look::from_view(&view);
    assert_eq!((look.text_scale, look.blur, look.dim), (1.6, 0.0, 0.8));
    assert!(!look.dark(true));
}

#[test]
fn backgrounds_parse_as_the_mac_app_reads_them() {
    assert_eq!(parse_background("none"), Some(Wallpaper::None));
    assert_eq!(parse_background("desktop"), Some(Wallpaper::Desktop));
    assert_eq!(parse_background("image: uploads/sea.png"), Some(Wallpaper::Image("uploads/sea.png".into())));
    assert_eq!(parse_background("gradient: 135deg, #000000 0%, #ffffff 100%"), Some(Wallpaper::Linear { angle: 135.0, stops: vec![([0.0; 3], 0.0), ([1.0; 3], 1.0)] }));
    let Some(Wallpaper::Mesh { columns, rows, colors }) = parse_background("mesh: 2x2, #000000, #ffffff, #ff0000, #00ff00") else { panic!() };
    assert_eq!((columns, rows, colors.len()), (2, 2, 4));
    assert_eq!(parse_background("mesh: 2x2, #000000"), None);
    assert_eq!(parse_background("gradient: 90deg, #000000 0%"), None);
    assert_eq!(parse_background("gradient: dusk"), None);
    assert_eq!(parse_background("image:"), None);
}

#[test]
fn images_are_placed_by_fit_and_position() {
    let p = place(Fit::Cover, Position::Center, (200.0, 100.0), (100.0, 100.0));
    assert_eq!(p.rect, [-50.0, 0.0, 200.0, 100.0]);
    assert_eq!(place(Fit::Cover, Position::Left, (200.0, 100.0), (100.0, 100.0)).rect, [0.0, 0.0, 200.0, 100.0]);
    assert_eq!(place(Fit::Cover, Position::Right, (200.0, 100.0), (100.0, 100.0)).rect, [-100.0, 0.0, 200.0, 100.0]);
    assert_eq!(place(Fit::Contain, Position::Top, (200.0, 100.0), (100.0, 100.0)).rect, [0.0, 0.0, 100.0, 50.0]);
    assert_eq!(place(Fit::Contain, Position::Bottom, (200.0, 100.0), (100.0, 100.0)).rect, [0.0, 50.0, 100.0, 50.0]);
    assert_eq!(place(Fit::Center, Position::Center, (40.0, 20.0), (100.0, 100.0)).rect, [30.0, 40.0, 40.0, 20.0]);
    let tile = place(Fit::Tile, Position::Right, (40.0, 20.0), (100.0, 100.0));
    assert!(tile.tile);
    assert_eq!(tile.rect, [0.0, 0.0, 40.0, 20.0]);
}

#[test]
fn panes_darken_only_as_far_as_text_needs() {
    let roles = [luminance(hex("#ececee").unwrap()), luminance(hex("#a3a3a8").unwrap())];
    let edge = boundary(&roles, true, 4.5);
    let ground = hex("#161618").unwrap();
    let bright = [0.9; 3];
    let a = pane_alpha(bright, ground, edge, true, 0.0);
    assert!(a > 0.3 && a < 1.0, "{a}");
    let shown = luminance(blend(bright, ground, a));
    for l in roles {
        assert!(contrast(l, shown) >= 4.5 - 1e-3, "{}", contrast(l, shown));
    }
    let barely = luminance(blend(bright, ground, a - 0.02));
    assert!(roles.iter().any(|l| contrast(*l, barely) < 4.5), "the tint is no darker than it needs to be");
    assert_eq!(pane_alpha([0.05; 3], ground, edge, true, 0.2), 0.2);
    assert!(pane_alpha(bright, ground, edge, true, 0.95) >= 0.95);

    let light_roles = [luminance(hex("#1d1d1f").unwrap())];
    let light_edge = boundary(&light_roles, false, 4.5);
    let paper = [0.97; 3];
    let dark_bg = [0.1; 3];
    let b = pane_alpha(dark_bg, paper, light_edge, false, 0.0);
    let shown = luminance(blend(dark_bg, paper, b));
    assert!(contrast(light_roles[0], shown) >= 4.5 - 1e-3);
}

#[test]
fn the_worst_area_is_pooled_over_the_blur_and_a_lone_pixel_counts_when_sharp() {
    let side = 960u32;
    let mut rgba = vec![20u8; (side * side * 4) as usize];
    for y in 0..40 {
        for x in 0..40 {
            let i = ((y * side + x) * 4) as usize;
            rgba[i..i + 4].copy_from_slice(&[250, 250, 250, 255]);
        }
    }
    let w = worst(&rgba, side, true, 10);
    assert!(w[0] > 0.9, "{w:?}");
    assert!((worst(&rgba, side, false, 10)[0] - 20.0 / 255.0).abs() < 1e-4);
    assert_eq!(worst(&[], 4, true, 1), [1.0; 3]);
    let mut speck = vec![20u8; (side * side * 4) as usize];
    speck[4..8].copy_from_slice(&[255, 255, 255, 255]);
    assert_eq!(worst(&speck, side, true, 1), [1.0; 3]);
    assert!(worst(&speck, side, true, 40)[0] < 0.1);
}

#[test]
fn a_tint_too_bright_for_the_text_is_darkened_until_it_can_work() {
    let roles = [luminance(hex("#ececee").unwrap())];
    let edge = boundary(&roles, true, 4.5);
    let pale = reachable([0.9, 0.9, 0.95], edge, true);
    assert!(luminance(pale) <= edge);
    let fine = hex("#161618").unwrap();
    assert_eq!(reachable(fine, edge, true), fine);
}

#[test]
fn a_mesh_runs_corner_to_corner() {
    let (w, h, px) = mesh(2, 2, &[[0.0; 3], [1.0; 3], [0.0; 3], [1.0; 3]], 2.0);
    assert_eq!((w, h), (192, 96));
    assert_eq!(&px[0..4], &[0, 0, 0, 255]);
    let last = ((w - 1) * 4) as usize;
    assert_eq!(&px[last..last + 4], &[255, 255, 255, 255]);
}

#[test]
fn changes_are_the_lines_the_daemon_checks() {
    let everywhere = Scope::Everywhere;
    let here = Scope::ThisComputer("Studio".into());
    assert_eq!(Change::set(&everywhere, "glass.blur", "12", "glass blur to 12").0, json!({"action": "set", "set": ["glass.blur = 12"], "message": "Set glass blur to 12"}));
    assert_eq!(Change::set(&here, "theme", "dark", "appearance to dark").0, json!({"action": "set", "set": ["theme@device:Studio = dark"], "message": "Set appearance to dark on Studio"}));
    let reset = Change::reset(&here).0;
    assert_eq!(reset["unset"].as_array().unwrap().len(), KEYS.len());
    assert_eq!(reset["unset"][0], "theme@device:Studio");
    assert_eq!(reset["message"], "Reset appearance on Studio");
    assert_eq!(Change::undo(None).0, json!({"action": "undo"}));
    assert_eq!(Change::undo(Some("abc")).0, json!({"action": "undo", "rev": "abc"}));
    assert_eq!(Change::clear_override("glass.blur", "mac", "All Macs").0, json!({"action": "unset", "unset": ["glass.blur@mac"], "message": "Reset glass.blur for All Macs"}));
    assert_eq!(owner("mac", "Studio"), "All Macs");
    assert_eq!(owner("device:Studio", "Studio"), "This computer");
    assert_eq!(owner("device:Pocket", "Studio"), "Pocket");
}

#[test]
fn history_names_who_and_when() {
    let now = parse_time("2026-11-26T12:00:00Z").unwrap();
    let e = |author: &str, time: &str| Entry { rev: "r".into(), author: author.into(), time: time.into(), message: "m".into() };
    assert_eq!(describe(&e("person", "2026-11-26T11:55:00Z"), now), "You · 5 min ago");
    assert_eq!(describe(&e("@collab", "2026-11-16T16:46:27-05:00"), now), "@collab · 9 d ago");
    assert_eq!(describe(&e("person", "2026-11-26T09:00:00Z"), now), "You · 3 h ago");
    assert_eq!(describe(&e("person", ""), now), "You");
}

#[test]
fn pane_tint_follows_strength_dim_and_the_readability_switch() {
    let view: View = serde_json::from_str(LIVE).unwrap();
    let mut look = Look::from_view(&view);
    let ground = hex("#161618").unwrap();
    let white = [1.0; 3];
    let full = look.pane_tint(true, [0.5; 3], white, ground, ground, 1.0, 0.0).0;
    assert!(full > 0.5, "{full}");
    assert!(look.pane_tint(true, [0.5; 3], white, ground, ground, 0.1, 0.0).0 < full);
    look.dim = 0.8;
    assert!(look.pane_tint(true, [0.5; 3], white, ground, ground, 1.0, 0.0).0 < full);
    let (a, t) = look.pane_tint(true, [0.5; 3], white, ground, [1.0; 3], 1.0, 0.0);
    assert!(a <= 1.0 && luminance(t) < luminance([1.0; 3]));
    look.readable = false;
    assert_eq!(look.pane_tint(true, [0.5; 3], white, ground, ground, 1.0, 0.25), (0.25, ground));
}

#[test]
fn sampling_keeps_the_brightest_and_darkest_pixel_of_each_area() {
    let (w, h) = (1000u32, 500u32);
    let mut rgba = vec![60u8; (w * h * 4) as usize];
    let at = ((250 * w + 777) * 4) as usize;
    rgba[at..at + 4].copy_from_slice(&[255, 255, 255, 255]);
    let at = ((10 * w + 3) * 4) as usize;
    rgba[at..at + 4].copy_from_slice(&[0, 0, 0, 255]);
    let (sw, sh, bright) = extreme(&rgba, w, h, 100, true);
    assert_eq!((sw, sh), (100, 50));
    assert_eq!(worst(&bright, sw, true, 1), [1.0; 3]);
    let (_, _, dark) = extreme(&rgba, w, h, 100, false);
    assert_eq!(worst(&dark, sw, false, 1), [0.0; 3]);
}

#[test]
fn increase_contrast_forces_readability_and_the_stronger_colours() {
    let view: View = serde_json::from_str(r##"{"rev":"r","values":{"glass.readability":"off","contrast.target":4.5},"colors":{"muted":{"dark":"#a3a3a8","light":"#55565c","increased":{"dark":"#b0b0b4","light":"#4b4c51"}}}}"##).unwrap();
    assert!(!Look::from_view(&view).readable);
    let look = Look::with_increased_contrast(&view);
    assert!(look.readable);
    assert_eq!(look.target, 7.0);
    assert_eq!(look.role("muted", true), hex("#b0b0b4"));
    assert_eq!(look.role("muted", false), hex("#4b4c51"));
}

#[test]
fn roles_on_the_system_accent_are_named() {
    let view: View = serde_json::from_str(r##"{"colors":{"accent":{"systemAccent":true},"body":{"systemAccent":true},"link":{"systemAccent":true},"muted":{"dark":"#a3a3a8","light":"#55565c"}}}"##).unwrap();
    let mut roles = Look::from_view(&view).accent_roles;
    roles.sort();
    assert_eq!(roles, vec!["body".to_string(), "link".to_string()]);
}

#[test]
fn text_in_the_system_accent_counts_toward_pane_tint() {
    let view: View = serde_json::from_str(r##"{"values":{"glass":"on"},"colors":{"body":{"systemAccent":true}}}"##).unwrap();
    let look = Look::from_view(&view);
    let ground = hex("#161618").unwrap();
    let pale = look.pane_tint(true, [0.95, 0.95, 1.0], [0.6; 3], ground, ground, 1.0, 0.0).0;
    let deep = look.pane_tint(true, [0.2, 0.25, 0.9], [0.6; 3], ground, ground, 1.0, 0.0).0;
    assert!(deep > pale, "{deep} {pale}");
}

#[test]
fn every_drawn_text_colour_counts_toward_pane_tint() {
    let quiet: View = serde_json::from_str(r##"{"colors":{"body":{"dark":"#ececee","light":"#1d1d1f"}}}"##).unwrap();
    let loud: View = serde_json::from_str(r##"{"colors":{"body":{"dark":"#ececee","light":"#1d1d1f"},"error":{"dark":"#b03030","light":"#b3241a"}}}"##).unwrap();
    let ground = hex("#161618").unwrap();
    let a = Look::from_view(&quiet).pane_tint(true, [0.9; 3], [0.5; 3], ground, ground, 1.0, 0.0).0;
    let b = Look::from_view(&loud).pane_tint(true, [0.9; 3], [0.5; 3], ground, ground, 1.0, 0.0).0;
    assert!(b > a, "{b} {a}");
}

#[test]
fn a_dark_system_accent_is_lifted_until_text_in_it_reads() {
    let surfaces = [hex("#161618").unwrap(), hex("#1f1f23").unwrap()];
    let navy = hex("#1a237e").unwrap();
    let lifted = readable_text(navy, &surfaces, true, 7.0);
    assert!(surfaces.iter().all(|s| contrast(luminance(lifted), luminance(*s)) >= 7.0));
    let bright = hex("#9ec5ff").unwrap();
    assert_eq!(readable_text(bright, &surfaces, true, 4.5), bright);
}

#[test]
fn increase_contrast_takes_the_stronger_explicit_accent() {
    let view: View = serde_json::from_str(r##"{"colors":{"accent":{"dark":"#4f8ff7","light":"#2459b8","increased":{"dark":"#8ab4ff","light":"#1a4490"}}}}"##).unwrap();
    assert_eq!(Look::from_view(&view).accent, Some((hex("#4f8ff7").unwrap(), hex("#2459b8").unwrap())));
    assert_eq!(Look::with_increased_contrast(&view).accent, Some((hex("#8ab4ff").unwrap(), hex("#1a4490").unwrap())));
}

#[test]
fn a_heic_picture_is_recognised_before_decoding() {
    let mut data = vec![0u8, 0, 0, 24];
    data.extend_from_slice(b"ftypheic");
    data.extend_from_slice(&[0; 16]);
    let got = thumbnail(&data, 64);
    if cfg!(target_os = "macos") {
        assert!(got.is_err());
    } else {
        assert_eq!(got.err().as_deref(), Some("HEIC pictures can be shown only on a Mac"));
    }
}

#[test]
fn a_bright_spot_across_two_cells_still_counts_in_full() {
    let side = 40u32;
    let mut rgba = vec![10u8; (side * side * 4) as usize];
    for y in 5..15 {
        for x in 5..15 {
            let i = ((y * side + x) * 4) as usize;
            rgba[i..i + 4].copy_from_slice(&[250, 250, 250, 255]);
        }
    }
    let w = worst(&rgba, side, true, 10);
    assert!(w[0] > 0.97, "{w:?}");
}

#[test]
fn a_photo_turned_by_its_orientation_tag_is_drawn_upright() {
    use image::{ImageEncoder, RgbImage};
    let mut wide = RgbImage::new(40, 20);
    for px in wide.pixels_mut() {
        *px = image::Rgb([200, 30, 30]);
    }
    let mut jpeg = Vec::new();
    image::codecs::jpeg::JpegEncoder::new_with_quality(&mut jpeg, 90).write_image(wide.as_raw(), 40, 20, image::ExtendedColorType::Rgb8).unwrap();
    let exif: [u8; 34] = [0xFF, 0xE1, 0x00, 0x20, b'E', b'x', b'i', b'f', 0, 0, b'M', b'M', 0, 0x2A, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, 0, 0];
    let mut tagged = jpeg[..2].to_vec();
    tagged.extend_from_slice(&exif);
    tagged.extend_from_slice(&jpeg[2..]);
    let d = decode(&tagged, 2560, 16).unwrap();
    assert_eq!(d.original, (20, 40));
    assert_eq!((d.shown.0, d.shown.1), (20, 40));
}
