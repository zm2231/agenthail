use std::cell::Cell;

use rune::{El, ScrollPos, Tx, col, row};
use rune_kit::{ControlSize, IconButton, IconButtonVariant, Kit};
use rune_theme_opencode::icons;

use super::Prefs;
use crate::theme;

const SIDE: f32 = 28.0;
const STRIP: &str = "settings-tabs";
const PAGE: f32 = 0.75;

#[derive(Clone, Copy, Default, PartialEq)]
struct Scrolled {
    offset: f32,
    max: f32,
    viewport: [f32; 2],
}

thread_local! {
    static SCROLLED: Cell<Scrolled> = Cell::new(Scrolled::default());
}

fn watch(at: ScrollPos) {
    let next = Scrolled { offset: at.offset[0], max: at.max[0], viewport: at.viewport };
    if SCROLLED.get() != next {
        SCROLLED.set(next);
        crate::wake::SETTINGS.ring();
    }
}

fn chevron(key: &'static str, glyph: &'static rune::Icon, label: &'static str, toward: f32, x: f32, height: f32, k: &Kit) -> El {
    let button = IconButton::new(glyph)
        .variant(IconButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .label(label)
        .on_press_tx(move |_: &mut Prefs, tx: &mut Tx| {
            let at = SCROLLED.get();
            tx.scroll_to_x(STRIP, (at.offset + toward * at.viewport[0] * PAGE).clamp(0.0, at.max));
        })
        .build(k);
    row().key(key).absolute(x, 0.0).w(SIDE).h(height).items_center().justify_center().fill(theme::card_deep()).child(button)
}

pub fn strip(tabs: El, k: &Kit) -> El {
    let at = SCROLLED.get();
    let scroller = col().key(STRIP).w_full().scroll_x().scroll_edge_fade(24.0).on_scroll_offset(watch).child(col().w_full().items_center().pt(14.0).pb(10.0).child(tabs));
    let mut band = col().w_full().child(scroller);
    let [width, height] = at.viewport;
    if at.offset > 0.5 {
        band = band.child(chevron("settings-tabs-before", &icons::CHEVRON_LEFT, "Earlier tabs", -1.0, 0.0, height, k));
    }
    if at.max - at.offset > 0.5 {
        band = band.child(chevron("settings-tabs-after", &icons::CHEVRON_RIGHT, "More tabs", 1.0, width - SIDE, height, k));
    }
    band
}
