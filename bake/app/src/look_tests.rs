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

fn contrast(a: Color, b: Color) -> f32 {
    let (a, b) = (a.luminance(), b.luminance());
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

fn kit_surfaces(k: &Kit, behind: Color) -> [Color; 3] {
    let deep = over(k.c.bg_deep, behind);
    [over(k.c.bg_base, deep), deep, over(k.c.layer_01, deep)]
}

fn reads(color: Color, on: &[Color], min: f32) -> bool {
    on.iter().all(|&s| contrast(color, s) >= min * 0.999)
}

fn dim_text(dark: bool) -> Color {
    if dark { rune::oklch(0.42, 0.01, 270.0) } else { rune::oklch(0.72, 0.01, 270.0) }
}

#[test]
fn kits_follow_the_system_increased_contrast_and_keep_colours_that_already_read() {
    for paint in [Paint::dark(), Paint::light()] {
        let roles = Kit::neutral(paint.dark).roles;
        let behind = paint.card_deep;
        for translucent in [false, true] {
            let normal = KitEnv::new(paint.dark);
            let increased = normal.increased_contrast(true);
            let plain = main_kit(&normal, &paint, roles, translucent, behind);
            let main = main_kit(&increased, &paint, roles, translucent, behind);
            assert!(!plain.increased_contrast);
            assert!(main.increased_contrast, "code and diffs in the main kit hold 7:1");
            assert_ne!(main.c.icon_muted, plain.c.icon_muted, "the kit's own muted icons lift");
            assert_eq!((plain.c.text_base, plain.c.text_muted, plain.c.text_faint), (paint.text, paint.muted, paint.faint), "colours that read come back as they are");
            let on = kit_surfaces(&main, behind);
            for (got, served, min) in [(main.c.text_base, paint.text, 7.0), (main.c.text_muted, paint.muted, 7.0), (main.c.text_faint, paint.faint, 4.5)] {
                assert!(reads(got, &on, min), "under increased contrast text reads at {min}:1");
                assert_eq!(got == served, reads(served, &on, min), "only a colour that falls short moves");
            }
            let plain = chrome_kit(&normal, &paint, roles, translucent, true, behind);
            let chrome = chrome_kit(&increased, &paint, roles, translucent, true, behind);
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
            let main = main_kit(&env, &paint, roles, false, behind);
            let on = kit_surfaces(&main, behind);
            assert!(!reads(paint.muted, &on, min), "the served colour starts below {min}:1");
            assert!(reads(main.c.text_muted, &on, min), "muted text is lifted to {min}:1");
            assert!(reads(main.roles.meta, &on, min), "timestamps are lifted to {min}:1");
            assert_eq!(main.roles.link == roles.link, reads(roles.link, &on, min), "only a role that falls short moves");
            let chrome = chrome_kit(&env, &paint, roles, false, true, behind);
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
            hold_paint(&mut paint, &mut roles, floor, min, increased);
            let on = legible::surfaces(&paint, floor);
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
    let main = main_kit(&KitEnv::new(true), &paint, roles, true, behind);
    assert_eq!(main.c.bg_base, Color::WHITE.alpha(0.025), "the kit keeps its translucent surfaces");
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
    let key = kit_key(&paint, &roles, true, true, behind);
    assert_eq!(key, kit_key(&paint, &roles, true, true, behind));
    let nudged = |c: Color| c.mix(Color { r: 0.2, g: 0.2, b: 0.2, a: c.a }, 0.3);
    for moved in [
        Paint { accent: nudged(paint.accent), ..paint },
        Paint { text: nudged(paint.text), ..paint },
        Paint { pane: paint.pane.alpha(0.5), ..paint },
        Paint { border: Color::WHITE.alpha(0.2), ..paint },
        Paint { card_deep: nudged(paint.card_deep), ..paint },
    ] {
        assert_ne!(kit_key(&moved, &roles, true, true, behind), key, "a lifted accent, text or surface reaches the kit");
    }
    assert_ne!(kit_key(&paint, &roles, true, true, nudged(behind)), key, "what shows through the glass reaches the kit");
    let link = TextRoles { link: nudged(roles.link), ..roles };
    assert_ne!(kit_key(&paint, &link, true, true, behind), key);
}
