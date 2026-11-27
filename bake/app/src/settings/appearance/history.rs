use feed::look::{self as fl, Change};
use rune::{El, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

use super::{Prefs, scope, write, writing};
use crate::settings::parts;
use crate::theme::{self, BODY, META, muted};

pub(super) fn history(s: &Prefs) -> El {
    let k = theme::kit();
    let now = time::OffsetDateTime::now_utc();
    let entries = match &s.appearance.history {
        Some(Ok(e)) => e.clone(),
        _ => Vec::new(),
    };
    let last = match entries.first() {
        Some(e) => col().gap(2.0).grow(1.0).min_w(0.0).child(text(e.message.clone()).font(BODY).color(theme::text()).truncate()).child(text(fl::describe(e, now)).font(META).color(muted())),
        None => col().grow(1.0).child(text("No changes yet").font(BODY).color(muted())),
    };
    let busy = writing(s);
    let undo = Button::new("Undo").control_size(ControlSize::Small).disabled(busy || entries.len() < 2).on_press(|s: &mut Prefs| write(s, Change::undo(None))).build(k).key("appearance-undo");
    let reset = Button::new("Reset to Default…").control_size(ControlSize::Small).disabled(busy).on_press(|s: &mut Prefs| s.appearance.confirm_reset = true).build(k).key("appearance-reset");
    let mut rows = vec![row().w_full().gap(10.0).items_center().px(14.0).py(10.0).child(last).child(undo).child(reset)];
    if s.appearance.confirm_reset {
        let message = if s.appearance.here { "This resets the settings on this page made for this computer only. Text colours and other ui.conf lines stay." } else { "This resets the settings on this page for all devices. Lines that would still override them here go too, including ones for all Macs, so other Macs change as well. Values set for one other device stay, and so do text colours and other ui.conf lines." };
        let confirm = Button::new("Reset").variant(ButtonVariant::Danger).control_size(ControlSize::Small).disabled(busy).on_press(|s: &mut Prefs| {
            let change = Change::reset(&scope(s));
            write(s, change);
        });
        let cancel = Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut Prefs| s.appearance.confirm_reset = false);
        rows.push(
            row()
                .w_full()
                .gap(10.0)
                .items_center()
                .px(14.0)
                .py(10.0)
                .key("appearance-reset-confirm")
                .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text("Reset appearance to the defaults?").font(BODY).color(theme::text())).child(text(message).font(META).color(muted()).wrap()))
                .child(cancel.build(k).key("appearance-reset-cancel"))
                .child(confirm.build(k).key("appearance-reset-go")),
        );
    }
    if entries.len() > 1 {
        let open = s.appearance.history_open;
        rows.push(
            row()
                .w_full()
                .px(14.0)
                .py(8.0)
                .key("appearance-history-toggle")
                .role("button")
                .on_press(|s: &mut Prefs| s.appearance.history_open = !s.appearance.history_open)
                .child(text(if open { "Hide History" } else { "Show History" }).font(BODY).color(theme::text())),
        );
        if open {
            for (i, e) in entries.iter().enumerate().skip(1) {
                let rev = e.rev.clone();
                let undo = Button::new("Undo").control_size(ControlSize::Small).variant(ButtonVariant::Ghost).disabled(busy).on_press(move |s: &mut Prefs| write(s, Change::undo(Some(&rev)))).build(k).key(format!("appearance-undo-{i}"));
                rows.push(
                    row()
                        .w_full()
                        .gap(10.0)
                        .items_center()
                        .px(14.0)
                        .py(6.0)
                        .child(col().grow(1.0).min_w(0.0).gap(2.0).child(text(e.message.clone()).font(BODY).color(theme::text()).truncate()).child(text(fl::describe(e, now)).font(META).color(muted())))
                        .child(undo),
                );
            }
        }
    }
    if let Some(Err(f)) = &s.appearance.history {
        rows.push(parts::problem(format!("Couldn't read the change history: {}", f.message)));
    }
    parts::section("", rows, None)
}
