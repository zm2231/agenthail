use feed::catalog::Session;
use feed::holders::{self, pins};
use rune::{El, icon, row, text};
use rune_kit::{Kit, Menu, MenuItem, Placement};

use crate::theme::{META, accent};
use crate::{App, icons};

pub fn picker(s: &App, x: &Session, k: &Kit) -> Option<El> {
    let shared = x.shared_holders();
    if shared.is_empty() {
        return None;
    }
    let picked = pins().picked(x);
    let words = picked.map_or("Choose a terminal".to_string(), |h| format!("pid {}", h.pid));
    let said = picked.map_or(format!("Open in {} terminals. Choose a terminal", shared.len()), |h| format!("Sending to terminal {}", h.label()));
    let id = x.id.clone();
    let open = s.holder_menu.as_deref() == Some(x.id.as_str());
    Some(
        row()
            .key("holder-picker-trigger")
            .gap(5.0)
            .items_center()
            .px(8.0)
            .py(3.0)
            .radius(7.0)
            .hover(rune::Color::WHITE.alpha(0.08))
            .role("button")
            .label(said)
            .help(holders::summary(shared))
            .on_press(move |s: &mut App| s.holder_menu = if open { None } else { Some(id.clone()) })
            .child(icon(&icons::TERMINAL, k.text_size(12.0)).color(accent()))
            .child(text(words).font(k.text(META)).color(accent())),
    )
}

pub fn overlays(s: &App, layer: &mut Vec<El>, k: &Kit) {
    let Some(x) = s.holder_menu.as_deref().and_then(|id| crate::session(s, id)) else { return };
    let shared = x.shared_holders();
    if shared.is_empty() {
        return;
    }
    let picked = pins().picked(x).map(|h| h.pid);
    let id = x.id.clone();
    let mut m = Menu::new("holder-picker").open(true).min_width(220.0).placement(Placement::TopStart).on_close(|s: &mut App| s.holder_menu = None).group_label("Terminal");
    let none = id.clone();
    m = m.item(MenuItem::new("Choose a terminal").checked(picked.is_none()).on_select(move |s: &mut App| pick(s, &none, None)));
    for h in shared {
        let (id, pin) = (id.clone(), h.pin());
        m = m.item(MenuItem::new(h.label()).checked(picked == Some(h.pid)).on_select(move |s: &mut App| pick(s, &id, Some(pin))));
    }
    let _ = m.build(k, layer);
}

fn pick(s: &mut App, id: &str, pin: Option<holders::Pin>) {
    pins().pick(id, pin);
    s.holder_menu = None;
}
