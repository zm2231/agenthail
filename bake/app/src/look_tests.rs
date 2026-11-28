use super::*;

#[test]
fn the_same_appearance_problem_posted_again_does_not_redraw() {
    let (tx, rx) = crate::wake::channel();
    let mut s = State { rx: Some(rx), ..State::default() };
    let problem = Some("Couldn't read appearance settings: down".to_string());
    tx.send(Got::Problem(problem.clone())).unwrap();
    pump(&mut s);
    let shown = s.generation;
    tx.send(Got::Problem(problem)).unwrap();
    pump(&mut s);
    assert_eq!(s.generation, shown, "a retry that fails the same way changes nothing");
    tx.send(Got::Problem(None)).unwrap();
    pump(&mut s);
    assert_eq!(s.generation, shown + 1, "the problem clearing redraws");
}

use legible::contrast;

fn kit_surfaces(k: &Kit, behind: Color) -> [Color; 3] {
    let deep = over(k.c.bg_deep, behind);
    [over(k.c.bg_base, deep), deep, over(k.c.layer_01, deep)]
}

fn reads(color: Color, on: &[Color], min: f32) -> bool {
    legible::reads(color, on, min * 0.999)
}

fn one_sided_best(on: &[Color]) -> f32 {
    let lums = on.iter().map(|s| s.luminance());
    let (lo, hi) = lums.fold((f32::INFINITY, 0.0f32), |(lo, hi), l| (lo.min(l), hi.max(l)));
    ((lo + 0.05) / 0.05).max(1.05 / (hi + 0.05))
}

fn as_well_as_any(color: Color, on: &[Color], min: f32) -> bool {
    let l = color.luminance();
    let side = on.iter().all(|s| l <= s.luminance() + 1e-6) || on.iter().all(|s| l + 1e-6 >= s.luminance());
    let least = on.iter().map(|&s| contrast(color, s)).fold(f32::INFINITY, f32::min);
    side && (least >= min * 0.999 || least >= one_sided_best(on) * 0.999)
}

fn dim_text(dark: bool) -> Color {
    if dark { rune::oklch(0.42, 0.01, 270.0) } else { rune::oklch(0.72, 0.01, 270.0) }
}

#[test]
fn kits_follow_the_system_increased_contrast_and_keep_colours_that_already_read() {
    for paint in [Paint::dark(), Paint::light()] {
        let roles = Kit::neutral(paint.dark).roles;
        let behind = paint.card_deep;
        let behinds = [behind; 3];
        for translucent in [false, true] {
            let normal = KitEnv::new(paint.dark);
            let increased = normal.increased_contrast(true);
            let plain = main_kit(&normal, &paint, roles, translucent, behinds);
            let main = main_kit(&increased, &paint, roles, translucent, behinds);
            assert!(!plain.increased_contrast);
            assert!(main.increased_contrast, "code and diffs in the main kit hold 7:1");
            assert_ne!(main.c.icon_muted, plain.c.icon_muted, "the kit's own muted icons lift");
            assert_eq!((plain.c.text_base, plain.c.text_muted, plain.c.text_faint), (paint.text, paint.muted, paint.faint), "colours that read come back as they are");
            let on = kit_surfaces(&main, behind);
            for (got, served, min) in [(main.c.text_base, paint.text, 7.0), (main.c.text_muted, paint.muted, 7.0), (main.c.text_faint, paint.faint, 4.5)] {
                assert!(reads(got, &on, min), "under increased contrast text reads at {min}:1");
                assert_eq!(got == served, reads(served, &on, min), "only a colour that falls short moves");
            }
            let plain = chrome_kit(&normal, &paint, roles, translucent, true, behinds);
            let chrome = chrome_kit(&increased, &paint, roles, translucent, true, behinds);
            assert!(!plain.increased_contrast);
            assert!(chrome.increased_contrast, "code and diffs in the chrome kit hold 7:1");
            assert_eq!((plain.c.text_base, plain.c.text_weak), (paint.text, paint.muted));
            assert!(reads(chrome.c.text_weak, &kit_surfaces(&chrome, behind), 7.0));
        }
    }
}

#[test]
fn served_text_that_falls_short_is_lifted_to_body_contrast() {
    for dark in [true, false] {
        let mut paint = if dark { Paint::dark() } else { Paint::light() };
        paint.muted = dim_text(dark);
        let mut roles = Kit::neutral(dark).roles;
        roles.meta = dim_text(dark);
        let behind = paint.card_deep;
        for (env, min) in [(KitEnv::new(dark), 4.5), (KitEnv::new(dark).increased_contrast(true), 7.0)] {
            let main = main_kit(&env, &paint, roles, false, [behind; 3]);
            let on = kit_surfaces(&main, behind);
            assert!(!reads(paint.muted, &on, min), "the served colour starts below {min}:1");
            assert!(reads(main.c.text_muted, &on, min), "muted text is lifted to {min}:1");
            assert!(reads(main.roles.meta, &on, min), "timestamps are lifted to {min}:1");
            assert_eq!(main.roles.link == roles.link, reads(roles.link, &on, min), "only a role that falls short moves");
            let chrome = chrome_kit(&env, &paint, roles, false, true, [behind; 3]);
            assert!(reads(chrome.c.text_weak, &kit_surfaces(&chrome, behind), min));
        }
    }
}

#[test]
fn text_on_the_apps_own_cards_and_glass_is_held_to_body_contrast() {
    for dark in [true, false] {
        for (increased, min) in [(false, 4.5), (true, 7.0)] {
            let mut paint = if dark { Paint::dark() } else { Paint::light() };
            paint.muted = dim_text(dark);
            paint.failed = dim_text(dark);
            paint.accent = if dark { rune::oklch(0.38, 0.12, 268.0) } else { rune::oklch(0.8, 0.1, 268.0) };
            let text = paint.text;
            let mut roles = Kit::neutral(dark).roles;
            roles.meta = dim_text(dark);
            let floor = paint.card_deep;
            hold_paint(&mut paint, &mut roles, &[floor], min, increased);
            let on = legible::surfaces(&paint, &[floor]);
            assert!(reads(paint.muted, &on, min) && reads(paint.failed, &on, min) && reads(paint.accent, &on, min) && reads(roles.meta, &on, min), "served text reads at {min}:1 on cards, raised rows and glass");
            assert!(reads(paint.faint, &on, legible::faint_floor(increased)));
            assert_eq!(paint.text, text, "body text that reads is unchanged");
        }
    }
}

#[test]
fn light_text_on_dark_glass_is_measured_on_what_shows_through() {
    let paint = Paint { pane: rune::oklch(0.16, 0.008, 275.0).alpha(0.5), ..Paint::dark() };
    let roles = Kit::neutral(true).roles;
    let behind = rune::oklch(0.2, 0.02, 250.0);
    let main = main_kit(&KitEnv::new(true), &paint, roles, true, [behind; 3]);
    assert_eq!(main.c.bg_base, WASH, "the kit keeps its translucent surfaces");
    assert_eq!((main.c.text_base, main.c.text_muted), (paint.text, paint.muted), "a faint white wash is not read as a white surface");
}

#[test]
fn a_translucent_surface_is_measured_as_the_window_blends_it() {
    let half = over(Color::WHITE.alpha(0.5), Color::BLACK);
    assert!((half.r - 0.214).abs() < 0.002 && half.a == 1.0, "half white over black is sRGB 0.5, not linear 0.5: {half:?}");
    let pane = Color { r: 0.02, g: 0.03, b: 0.05, a: 1.0 };
    assert_eq!(over(pane, Color::WHITE), pane, "an opaque surface is itself");
}

#[test]
fn kits_are_rebuilt_when_any_colour_they_draw_with_moves() {
    let paint = Paint::dark();
    let roles = Kit::neutral(true).roles;
    let behind = paint.card_deep;
    let key = kit_key(&paint, &roles, true, true, [behind; 3]);
    assert_eq!(key, kit_key(&paint, &roles, true, true, [behind; 3]));
    let nudged = |c: Color| c.mix(Color { r: 0.2, g: 0.2, b: 0.2, a: c.a }, 0.3);
    for moved in [
        Paint { accent: nudged(paint.accent), ..paint },
        Paint { text: nudged(paint.text), ..paint },
        Paint { pane: paint.pane.alpha(0.5), ..paint },
        Paint { border: Color::WHITE.alpha(0.2), ..paint },
        Paint { card_deep: nudged(paint.card_deep), ..paint },
    ] {
        assert_ne!(kit_key(&moved, &roles, true, true, [behind; 3]), key, "a lifted accent, text or surface reaches the kit");
    }
    assert_ne!(kit_key(&paint, &roles, true, true, [behind, behind, nudged(behind)]), key, "what shows through the glass reaches the kit");
    let link = TextRoles { link: nudged(roles.link), ..roles };
    assert_ne!(kit_key(&paint, &link, true, true, [behind; 3]), key);
}

#[test]
fn text_over_a_light_floor_and_dark_glass_stays_clear_of_everything_between() {
    let light = Color { r: 0.9, g: 0.9, b: 0.9, a: 1.0 };
    let dark = Color { r: 0.01, g: 0.01, b: 0.012, a: 1.0 };
    let grey = Color { r: 0.2, g: 0.2, b: 0.2, a: 1.0 };
    for surfaces in [[light, dark], [dark, light]] {
        let held = legible::held(grey, &surfaces, 4.5);
        assert!(as_well_as_any(held, &surfaces, 4.5), "{held:?} sits lighter or darker than every part of {surfaces:?}, as far from them as any colour can");
    }
    let half = Color { r: 0.5, g: 0.5, b: 0.5, a: 1.0 };
    let pale = [Color::WHITE, half];
    let held = legible::held(grey, &pale, 4.5);
    assert!(legible::fits(held, &pale, 4.5), "{held:?} reads on white and mid grey");
    assert!(contrast(held, half) < 4.5 * 1.02, "text darkens only as far as it must: {}:1 on the darker surface", contrast(held, half));
    let mid = Color { r: 0.18, g: 0.18, b: 0.18, a: 1.0 };
    assert!(!legible::fits(mid, &[Color::WHITE, Color::BLACK], 4.5), "a grey between the brightest and darkest parts can vanish over the middle of the wallpaper");
}

#[test]
fn text_no_colour_can_hold_to_the_floor_on_every_surface_gets_the_most_contrast_it_can() {
    let grey = Color { r: 0.2, g: 0.2, b: 0.2, a: 1.0 };
    let wide = [Color { r: 0.1, g: 0.1, b: 0.1, a: 1.0 }, Color { r: 0.4, g: 0.4, b: 0.4, a: 1.0 }];
    let held = legible::held(grey, &wide, 4.5);
    assert_eq!(held, Color::BLACK, "black reaches 3:1 on the darker part, white only 2.3:1 on the brighter");
    assert!(!reads(held, &wide, 4.5) && as_well_as_any(held, &wide, 4.5), "short of 4.5:1, but no colour does better without sitting between the two");
    assert_eq!(legible::held(grey, &[Color::WHITE, Color::BLACK], 4.5), Color::BLACK);
}

#[test]
fn kit_text_over_glass_on_a_light_and_a_dark_part_of_the_wallpaper_reads_on_both() {
    let paint = Paint { pane: rune::oklch(0.16, 0.008, 275.0).alpha(0.3), ..Paint::dark() };
    let roles = Kit::neutral(true).roles;
    let behinds = [Color { r: 0.85, g: 0.85, b: 0.85, a: 1.0 }, Color { r: 0.004, g: 0.004, b: 0.005, a: 1.0 }, Color { r: 0.85, g: 0.85, b: 0.85, a: 1.0 }];
    for (env, min) in [(KitEnv::new(true), 4.5), (KitEnv::new(true).increased_contrast(true), 7.0)] {
        for k in [main_kit(&env, &paint, roles, true, behinds), chrome_kit(&env, &paint, roles, true, true, behinds)] {
            let on: Vec<Color> = behinds.iter().flat_map(|&b| kit_surfaces(&k, b)).collect();
            for got in [k.c.text_base, k.c.text_muted, k.c.text_weak] {
                assert!(as_well_as_any(got, &on, min), "{got:?} reads on every part of the glass as well as any colour can");
            }
            for role in rune_kit::TextRole::ALL {
                assert!(as_well_as_any(k.roles.get(role), &on, min), "{role:?} reads on every part of the glass as well as any colour can");
            }
        }
    }
}

#[test]
fn glass_is_measured_over_the_wallpapers_brightest_and_darkest_parts() {
    let mut s = State::default();
    let stops = vec![([0.95, 0.95, 0.95], 0.0), ([0.4, 0.2, 0.2], 0.5), ([0.02, 0.02, 0.03], 1.0)];
    s.look.background = Wallpaper::Linear { angle: 90.0, stops: stops.clone() };
    assert_eq!(worst_of(&mut s, true, true), Some(stops[0].0));
    assert_eq!(worst_of(&mut s, true, false), Some(stops[2].0));
}

#[test]
fn bare_ground_beside_a_contained_or_see_through_picture_counts_as_backdrop() {
    let white = |see_through: bool| fl::Decoded { original: (400, 900), shown: (1, 1, vec![255; 4]), brightest: (1, 1, vec![255; 4]), darkest: (1, 1, vec![255; 4]), see_through };
    let ground = ground_rgb(true);
    let mut s = State { area: (1440.0, 900.0), scale: 1.0, screen: 2880, ..State::default() };
    s.look.background = Wallpaper::Image("art/tall.png".into());
    assert_eq!(samples(&mut s, true, ground)[2], ground, "ground shows until the picture arrives");
    s.pictures.insert("art/tall.png".into(), Ok(picture(white(false))));
    for (fit, bare) in [(Fit::Contain, true), (Fit::Center, true), (Fit::Cover, false), (Fit::Tile, false)] {
        s.look.fit = fit;
        let [bright, dim, under] = samples(&mut s, true, ground);
        assert_eq!((bright, dim), ([1.0; 3], [1.0; 3]), "{fit:?}");
        assert_eq!(under, if bare { ground } else { [1.0; 3] }, "{fit:?}: the ground beside the picture is a backdrop only where it shows");
    }
    s.pictures.insert("art/tall.png".into(), Ok(picture(white(true))));
    for fit in [Fit::Cover, Fit::Tile] {
        s.look.fit = fit;
        assert_eq!(samples(&mut s, true, ground)[2], ground, "{fit:?}: ground shows through clear pixels");
    }
}

#[test]
fn a_blurred_clear_patch_is_measured_as_the_ground_showing_through() {
    let clear = [0u8, 0, 0, 0];
    let white = [255u8, 255, 255, 255];
    let sample: Vec<u8> = [white, clear, clear, clear].concat();
    let mut p = picture(fl::Decoded { original: (2, 2), shown: (1, 1, vec![255; 4]), brightest: (2, 2, sample.clone()), darkest: (2, 2, sample), see_through: true });
    let ground = [0.5, 0.5, 0.5];
    let seen = p.worst(true, 2.0, (2.0, 2.0), ground);
    let expected = (1.0 + 3.0 * quant(0.5) as f32 / 255.0) / 4.0;
    assert!((seen[0] - expected).abs() < 1e-3, "white beside clear cells over mid grey averages with the grey: {seen:?} vs {expected}");
}

#[test]
fn faint_pixels_in_a_blurred_patch_are_measured_mostly_as_the_ground() {
    let faint = [0u8, 0, 0, 10];
    let white = [255u8, 255, 255, 255];
    let sample: Vec<u8> = [white, faint, faint, faint].concat();
    let mut p = picture(fl::Decoded { original: (2, 2), shown: (1, 1, vec![255; 4]), brightest: (2, 2, sample.clone()), darkest: (2, 2, sample), see_through: true });
    let seen = p.worst(true, 2.0, (2.0, 2.0), [0.5, 0.5, 0.5]);
    let grey = quant(0.5) as f32 * (1.0 - 10.0 / 255.0);
    let expected = (255.0 + 3.0 * grey.round()) / 4.0 / 255.0;
    assert!((seen[0] - expected).abs() < 2e-3, "a nearly clear black shows the grey under it: {seen:?} vs {expected}");
}
