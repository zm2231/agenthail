use rune::{Color, El, Key, KeyEvent, TextStyle, Tx, col, icon, row, text, text_input};
use rune_theme_opencode::icons;

use feed::derive::Hit;

use crate::over::ui::{self, BODY_MED, META, META_MED, kit, strong, weak};
use crate::App;
use crate::over::open_target;

pub fn hits(s: &App) -> Vec<Hit> {
    let q = s.ov.jump.as_ref().map(|e| e.text.as_str()).unwrap_or_default();
    s.ov.d.jump(q, &s.ov.needs, &s.ov.hands_on)
}

pub fn key(s: &mut App, ev: &KeyEvent, _tx: &mut Tx) -> bool {
    match ev.key {
        Key::Escape => {
            s.ov.jump = None;
            true
        }
        Key::Down => {
            s.ov.jump_ix = (s.ov.jump_ix + 1).min(hits(s).len().saturating_sub(1));
            true
        }
        Key::Up => {
            s.ov.jump_ix = s.ov.jump_ix.saturating_sub(1);
            true
        }
        Key::Enter => {
            let found = hits(s);
            let ix = s.ov.jump_ix.min(found.len().saturating_sub(1));
            if let Some(h) = found.into_iter().nth(ix) {
                open_target(s, &h.target);
            }
            true
        }
        _ => false,
    }
}

pub fn view(s: &App, layer: &mut Vec<El>) {
    let k = kit();
    let Some(edit) = &s.ov.jump else { return };
    let input = text_input(edit, TextStyle::new(16.0, 450.0).lh(22.0))
        .single_line()
        .key("jump-input")
        .label("Jump to")
        .grow(1.0)
        .shrink()
        .color(strong())
        .placeholder("Jump to a project, work item or session".to_string(), weak())
        .on_edit(|s: &mut App| s.ov.jump.as_mut().expect("jump open"))
        .on_input_key(|s: &mut App, ev: &KeyEvent, tx: &mut Tx| key(s, ev, tx));
    let search = row().w_full().gap(10.0).items_center().px(16.0).py(14.0).border_sides([0.0, 0.0, 1.0, 0.0], ui::rule()).child(icon(&icons::MAGNIFYING_GLASS, 18.0).color(k.c.icon_base)).child(input).child(ui::ghost_keys(&["esc"]));
    let mut list = col().w_full().gap(2.0).padding(8.0);
    let mut last = "";
    let found = hits(s);
    let ix = s.ov.jump_ix.min(found.len().saturating_sub(1));
    for (i, h) in found.into_iter().enumerate() {
        if h.group != last {
            list = list.child(text(h.group.to_string()).font(META_MED).color(weak()).px(10.0).pt(if last.is_empty() { 4.0 } else { 12.0 }).pb(4.0));
            last = h.group;
        }
        let active = i == ix;
        let t = h.target.clone();
        let mut r = row()
            .key(format!("hit-{i}"))
            .w_full()
            .gap(12.0)
            .items_center()
            .px(10.0)
            .py(8.0)
            .radius(8.0)
            .on_press(move |s: &mut App| open_target(s, &t))
            .child(ui::avatar(&h.avatar, 22.0))
            .child(col().grow(1.0).shrink().min_w(0.0).gap(2.0).child(text(h.title.clone()).font(BODY_MED).color(strong()).truncate()).child(text(h.status.clone()).font(META).color(weak()).truncate()))
            .child(text(h.badge.clone()).font(META).color(weak()));
        if active {
            r = r.fill(k.c.surface_raised_hover).child(icon(&icons::ENTER, 14.0).color(k.c.icon_base));
        } else {
            r = r.hover(k.c.surface_raised_hover);
        }
        list = list.child(r);
    }
    let foot = row()
        .w_full()
        .gap(14.0)
        .items_center()
        .px(16.0)
        .py(10.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], ui::rule())
        .child(row().gap(6.0).items_center().child(ui::ghost_keys(&["↑", "↓"])).child(text("move".to_string()).font(META).color(weak())))
        .child(row().gap(6.0).items_center().child(ui::ghost_keys(&["⏎"])).child(text("open".to_string()).font(META).color(weak())))
        .child(ui::spacer())
        .child(text("Projects first, then work, then sessions".to_string()).font(META).color(weak()));
    let panel = col()
        .key("jump")
        .w(680.0)
        .radius(16.0)
        .fill(k.c.surface_raised)
        .border(1.0, k.c.border_base)
        .shadow(0.0, 24.0, Color::BLACK.alpha(0.55))
        .child(search)
        .child(list)
        .child(foot);
    layer.push(
        col()
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .items_center()
            .pt(84.0)
            .fill(Color::BLACK.alpha(0.45))
            .on_press(|s: &mut App| s.ov.jump = None)
            .child(panel.on_press(|_: &mut App| {})),
    );
}
