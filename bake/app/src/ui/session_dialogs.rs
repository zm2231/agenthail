use feed::catalog::Session;
use feed::menu::{self, Act};
use rune::{El, Tx, col, el, icon, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Menu, MenuItem, Placement};

use crate::icons;
use crate::theme::{self, BODY, META, accent, failed, faint, muted};
use crate::ui::session_menu::{Confirm, Open, chips_for, choose, close_rename, current_alias, run, save_rename, set};
use crate::App;

pub fn marks(x: &Session) -> El {
    let mut r = row().gap(4.0).items_center();
    if x.unread() {
        r = r.child(el().size(7.0, 7.0).radius(3.5).fill(accent()));
    }
    if x.pinned() {
        r = r.child(icon(&icons::PIN, 11.0).color(faint()));
    }
    r
}

pub fn button(x: &Session) -> El {
    let id = x.id.clone();
    col()
        .key("session-menu-trigger")
        .size(30.0, 30.0)
        .radius(9.0)
        .items_center()
        .justify_center()
        .hover(rune::Color::WHITE.alpha(0.08))
        .label("Session actions")
        .on_press(move |s: &mut App| {
            s.menus.chip = None;
            s.menus.open = match &s.menus.open {
                Some(o) if o.session == id && o.at.is_none() => None,
                _ => Some(Open { session: id.clone(), at: None }),
            };
        })
        .child(icon(&icons::MORE, 16.0).color(muted()))
}

fn act_icon(act: &Act) -> &'static rune::Icon {
    match act {
        Act::Rename => &icons::CORNER,
        Act::Terminal => &icons::TERMINAL,
        Act::Call => &icons::PHONE,
        Act::Pr(_) | Act::PrUnlink(_) | Act::PrLink => &icons::PULL,
        Act::PrWatch(..) => &icons::ACTIVITY,
        Act::Pin(_) => &icons::PIN,
        Act::Read(_) => &icons::CHECK_CIRCLE,
        Act::Archive(_) => &icons::INBOX,
    }
}

fn session_menu(s: &App, open: &Open, layer: &mut Vec<El>) {
    let Some(x) = crate::ui::gone::shown(s, &open.session) else { return };
    let mut m = Menu::new("session-menu").open(true).min_width(220.0).on_close(|s: &mut App| s.menus.open = None);
    m = match open.at {
        Some(p) => m.at(p.x, p.y),
        None => m.placement(Placement::BottomEnd),
    };
    let waiting = s.menus.busy.contains(&(x.id.clone(), "menu"));
    for (i, group) in menu::entries(x).into_iter().enumerate() {
        if i > 0 {
            m = m.separator();
        }
        for entry in group {
            let (id, act) = (x.id.clone(), entry.act.clone());
            let mut item = MenuItem::new(entry.label).icon(act_icon(&entry.act)).disabled(!entry.enabled || waiting && !matches!(entry.act, Act::Pr(_)));
            if let Some(note) = entry.note {
                item = item.description(note);
            }
            m = m.item(item.on_select(move |s: &mut App| run(s, &id, act.clone())));
        }
    }
    let _ = m.build(theme::kit(), layer);
}

fn chip_menu(s: &App, id: &str, key: &'static str, layer: &mut Vec<El>) {
    let Some(chip) = chips_for(s, id).into_iter().find(|c| c.key == key) else { return };
    let mut m = Menu::new(format!("chip-{key}")).open(true).min_width(200.0).placement(Placement::BottomStart).on_close(|s: &mut App| s.menus.chip = None).group_label(chip.name);
    for choice in &chip.choices {
        let (id, chip_, choice_) = (id.to_string(), chip.clone(), choice.clone());
        m = m.item(MenuItem::new(choice.label.clone()).checked(choice.id == chip.value).on_select(move |s: &mut App| choose(s, &id, &chip_, &choice_)));
    }
    if chip.next_turn {
        m = m.separator().group_label("Applies from the next turn");
    }
    let _ = m.build(theme::kit(), layer);
}

fn rename_dialog(s: &App, id: &str, layer: &mut Vec<El>) {
    let k = theme::kit();
    let current = current_alias(s, id);
    let text_now = s.menus.rename_text.text.clone();
    let problem = menu::rename_problem(&text_now, current.as_deref());
    let unchanged = menu::unchanged(&text_now, current.as_deref());
    let clearing = menu::clears(&text_now, current.as_deref());
    let title = crate::ui::gone::shown(s, id).map(|x| x.title()).unwrap_or_default();
    let input = text_input(&s.menus.rename_text, BODY)
        .single_line()
        .key("rename-input")
        .label("Name")
        .grow(1.0)
        .min_w(0.0)
        .color(theme::text())
        .placeholder("name", faint())
        .on_edit(|s: &mut App| &mut s.menus.rename_text)
        .on_submit(|s: &mut App, _: &mut Tx| save_rename(s));
    let field = row().w_full().gap(6.0).items_center().px(10.0).py(7.0).radius(8.0).fill(theme::wash()).border(1.0, theme::line()).child(text("@").font(BODY).color(muted())).child(input);
    let mut body = col().w_full().gap(10.0).px(16.0).pb(16.0).child(field);
    if let Some(message) = s.menus.rename_error.clone().or_else(|| (!text_now.is_empty()).then_some(problem).flatten().map(String::from)) {
        body = body.child(text(message).font(META).color(failed()).wrap().w_full());
    }
    let save_label = if s.menus.rename_saving { "Saving…" } else if clearing { "Clear name" } else { "Save" };
    let buttons = row()
        .w_full()
        .gap(8.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(close_rename).build(k).key("rename-cancel"))
        .child(
            Button::new(save_label)
                .variant(ButtonVariant::Contrast)
                .control_size(ControlSize::Small)
                .disabled(s.menus.rename_saving || problem.is_some() || unchanged)
                .on_press(save_rename)
                .build(k)
                .key("rename-save"),
        );
    let description = if current.is_some() { "Other agents and links use this name to reach the session. Leave it empty to clear it." } else { "Other agents and links use this name to reach the session." };
    Dialog::new("rename", format!("Rename {title}")).description(description).open(true).fit().width(400.0).on_close(close_rename).child(body.child(buttons)).build(k, layer);
}

fn confirm_dialog(c: &Confirm, layer: &mut Vec<El>) {
    let k = theme::kit();
    let (id, key, value) = (c.session.clone(), c.key, c.choice.id.clone());
    let buttons = row()
        .w_full()
        .gap(8.0)
        .px(16.0)
        .pb(16.0)
        .child(el().grow(1.0))
        .child(Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut App| s.menus.confirm = None).build(k).key("confirm-cancel"))
        .child(Button::new(c.choice.label.clone()).variant(ButtonVariant::Danger).control_size(ControlSize::Small).on_press(move |s: &mut App| set(s, &id, key, &value)).build(k).key("confirm-switch"));
    Dialog::new("confirm-mode", format!("Switch to {}?", c.choice.label))
        .description("The agent will run tools without asking you first.")
        .open(true)
        .fit()
        .width(400.0)
        .on_close(|s: &mut App| s.menus.confirm = None)
        .child(buttons)
        .build(k, layer);
}

pub fn overlays(s: &App, layer: &mut Vec<El>) {
    if let Some(open) = &s.menus.open {
        session_menu(s, open, layer);
    }
    if let Some((id, key)) = &s.menus.chip {
        chip_menu(s, id, key, layer);
    }
    if let Some(id) = &s.menus.renaming {
        rename_dialog(s, id, layer);
    }
    if let Some(c) = &s.menus.confirm {
        confirm_dialog(c, layer);
    }
}
