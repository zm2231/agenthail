use feed::look as fl;
use rune::{El, text};
use rune_kit::SegmentedControl;

use super::Prefs;
use super::backgrounds::grid;
use super::controls::{accents, chosen, segmented, slider, switch};
use super::history::history;
use crate::settings::parts;
use crate::theme::{self, META};

pub fn page(s: &Prefs) -> El {
    let mut out = parts::page().key("appearance-page");
    let Some(view) = crate::look::view() else {
        return out.child(match crate::look::problem() {
            Some(p) => parts::problem(p),
            None if s.client.is_none() => parts::problem(super::super::unread(s).message),
            None => parts::reading("appearance settings"),
        });
    };
    if !view.problems.is_empty() {
        let rows = view.problems.iter().map(|p| parts::problem(format!("ui.conf line {}: {}", p.line, p.message))).collect();
        out = out.child(parts::section("Problems in ui.conf", rows, Some("These lines are skipped; the rest of the file still applies.".into())));
    }
    if let Some(p) = crate::look::problem() {
        out = out.child(text(p).font(META).color(theme::attention()).wrap().px(4.0));
    }
    if let Some(f) = s.appearance.error.clone() {
        out = out.child(parts::failure(s, &f));
    }
    let device = crate::look::device();
    let scope_control = SegmentedControl::new(&["All devices", "This computer only"], Some(usize::from(s.appearance.here)))
        .label("Apply changes to")
        .fit()
        .on_pick(|s: &mut Prefs, i, _| s.appearance.here = i == 1 && !crate::look::device().is_empty())
        .build(theme::kit())
        .key("appearance-scope");
    let scope_detail = (!device.is_empty()).then(|| format!("This computer is {device}."));
    out = out.child(parts::section(
        "",
        vec![
            parts::line("Apply changes to", scope_detail, scope_control),
            segmented(s, &view, "theme", "Appearance", &[("System", "system"), ("Light", "light"), ("Dark", "dark")], "system", false),
            accents(s, &view),
            slider(s, &view, "text.scale", "Text size", (0.8, 1.6), 1.0, "×", false),
        ],
        None,
    ));
    let is_image = chosen(&view, "background", "").starts_with("image:");
    let mut background = vec![grid(s, &view)];
    background.push(slider(s, &view, "background.dim", "Dim", (0.0, 0.8), 0.0, "", false));
    background.push(slider(s, &view, "background.opacity", "Strength on empty views", (0.0, 1.0), 1.0, "", false));
    background.push(slider(s, &view, "background.session-opacity", "Strength in a session", (0.0, 1.0), 1.0, "", false));
    if is_image {
        background.push(segmented(s, &view, "background.fit", "Fit", &[("Cover", "cover"), ("Contain", "contain"), ("Center", "center"), ("Tile", "tile")], "cover", false));
        background.push(segmented(s, &view, "background.position", "Position", &[("Center", "center"), ("Top", "top"), ("Bottom", "bottom"), ("Left", "left"), ("Right", "right")], "center", false));
    }
    out = out.child(parts::section("Background", background, None));
    let glass_off = chosen(&view, "glass", "on") != "on";
    out = out.child(parts::section(
        "Glass",
        vec![
            switch(s, &view, "glass", "Translucent panes", "on", false),
            slider(s, &view, "glass.blur", "Blur", (0.0, 64.0), 30.0, " pt", glass_off),
            slider(s, &view, "glass.opacity", "Pane tint", (0.0, 1.0), 0.3, "", glass_off),
            slider(s, &view, "glass.composer.opacity", "Floating controls tint", (0.0, 1.0), 0.5, "", glass_off),
            segmented(s, &view, "glass.tint", "Tint colour", &[("Window", "ground"), ("Accent", "accent")], "ground", glass_off),
            switch(s, &view, "glass.readability", "Keep text readable", "auto", glass_off),
        ],
        Some("Panes are translucent only in dark appearance; light appearance uses solid panes.".into()),
    ));
    let no_texture = chosen(&view, "texture", "none") == "none";
    out = out.child(parts::section(
        "Texture",
        vec![
            segmented(s, &view, "texture", "Texture", &[("None", "none"), ("Grain", "grain"), ("Scanlines", "scanlines")], "none", false),
            slider(s, &view, "texture.strength", "Strength", (0.0, 0.6), 0.2, "", no_texture),
        ],
        None,
    ));
    out = out.child(history(s));
    let notes = fl::NOT_DRAWN.iter().map(|n| parts::words(*n)).collect();
    out.child(parts::section("Not drawn in this app yet", notes, Some("These still apply on the other Agenthail apps.".into())))
}
