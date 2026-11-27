use feed::look::{self as fl, Wallpaper};
use rune::{El, Pixels, TextEdit, col, el, row, text};
use rune_kit::{Button, ButtonVariant, ColorPicker, ControlSize, Hsv};

use super::controls::chosen;
use super::{Prefs, set, writing};
use crate::theme::{self, META, muted};

const MESH: usize = 3;

pub(super) struct Pick {
    pub hsv: Hsv,
    pub hex: TextEdit,
}

pub(super) struct Mesh {
    pub colors: Vec<Hsv>,
    pub cell: usize,
    pub hex: TextEdit,
}

fn pick(hsv: Hsv) -> Pick {
    Pick { hsv, hex: TextEdit::new(&hsv.hex()) }
}

pub(super) fn open_accent(s: &mut Prefs) {
    let Some(view) = crate::look::view() else { return };
    let current = chosen(&view, "theme.accent", "system");
    let hsv = Hsv::from_hex(current).unwrap_or_else(|| Hsv::from_color(theme::accent()));
    s.appearance.accent_edit = Some(pick(hsv));
}

pub(super) fn accent_editor(s: &Prefs) -> Option<El> {
    let p = s.appearance.accent_edit.as_ref()?;
    let k = theme::kit();
    let hex = p.hsv.hex();
    let picker = ColorPicker::new(p.hsv)
        .label("Accent colour")
        .size(240.0, 150.0)
        .on_change(|s: &mut Prefs, hsv, _| {
            if let Some(p) = s.appearance.accent_edit.as_mut() {
                p.hsv = hsv;
                ColorPicker::sync_hex(&mut p.hex, hsv);
            }
        })
        .hex_field(&p.hex, |s: &mut Prefs| &mut s.appearance.accent_edit.get_or_insert_with(|| pick(Hsv::new(220.0, 0.6, 0.9))).hex)
        .build(k);
    let apply = Button::new("Use Colour")
        .control_size(ControlSize::Small)
        .disabled(writing(s))
        .on_press(move |s: &mut Prefs| {
            s.appearance.accent_edit = None;
            set(s, "theme.accent", &hex, &format!("accent to {hex}"));
        })
        .build(k)
        .key("appearance-accent-use");
    let cancel = Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut Prefs| s.appearance.accent_edit = None).build(k).key("appearance-accent-cancel");
    Some(row().w_full().px(14.0).py(10.0).gap(16.0).key("appearance-accent-editor").child(picker).child(col().gap(8.0).child(apply).child(cancel)))
}

fn mesh_colors(value: &str) -> Option<Vec<Hsv>> {
    match fl::parse_background(value)? {
        Wallpaper::Mesh { columns: MESH, rows: MESH, colors } => Some(colors.into_iter().map(Hsv::from_srgb).collect()),
        _ => None,
    }
}

pub(super) fn open_mesh(s: &mut Prefs) {
    let Some(view) = crate::look::view() else { return };
    let dark = theme::paint().dark;
    let current = view.values.get(if dark { "background" } else { "background.light" }).and_then(|v| v.as_str()).or_else(|| view.values.get("background").and_then(|v| v.as_str())).unwrap_or_default();
    let fallback = match &s.appearance.presets {
        Some(Ok(p)) => p.iter().find(|p| p.value == "gradient: dusk").map(|p| if dark { p.dark.clone() } else { p.light.clone() }),
        _ => None,
    };
    let colors = mesh_colors(current).or_else(|| fallback.as_deref().and_then(mesh_colors)).unwrap_or_else(|| vec![Hsv::new(250.0, 0.5, 0.3); MESH * MESH]);
    let hex = TextEdit::new(&colors[0].hex());
    s.appearance.mesh_edit = Some(Mesh { colors, cell: 0, hex });
}

pub(super) fn mesh_value(colors: &[Hsv]) -> String {
    format!("mesh: {MESH}x{MESH}, {}", colors.iter().map(|c| c.hex()).collect::<Vec<_>>().join(", "))
}

pub(super) fn mesh_editor(s: &Prefs) -> Option<El> {
    let m = s.appearance.mesh_edit.as_ref()?;
    let k = theme::kit();
    let mut grid = col().gap(6.0);
    for r in 0..MESH {
        let mut line = row().gap(6.0);
        for c in 0..MESH {
            let i = r * MESH + c;
            let ring = if i == m.cell { k.c.border_focus } else { theme::line() };
            line = line.child(
                el().size(36.0, 36.0)
                    .radius(8.0)
                    .padding(2.0)
                    .border(if i == m.cell { 2.5 } else { 1.0 }, ring)
                    .key(format!("appearance-mesh-cell-{i}"))
                    .role("button")
                    .label(format!("Colour {}", i + 1))
                    .checked(i == m.cell)
                    .on_press(move |s: &mut Prefs| {
                        if let Some(m) = s.appearance.mesh_edit.as_mut() {
                            m.cell = i;
                            m.hex = TextEdit::new(&m.colors[i].hex());
                        }
                    })
                    .child(el().w_full().h_full().radius(6.0).fill(m.colors[i].to_color())),
            );
        }
        grid = grid.child(line);
    }
    let rgb: Vec<fl::Rgb> = m.colors.iter().map(|c| c.to_srgb()).collect();
    let (w, h, rgba) = fl::mesh(MESH, MESH, &rgb, 1.6);
    let preview = Pixels::new();
    preview.set(w, h, rgba);
    let picker = ColorPicker::new(m.colors[m.cell])
        .label("Gradient colour")
        .size(200.0, 120.0)
        .on_change(|s: &mut Prefs, hsv, _| {
            if let Some(m) = s.appearance.mesh_edit.as_mut() {
                let cell = m.cell;
                m.colors[cell] = hsv;
                ColorPicker::sync_hex(&mut m.hex, hsv);
            }
        })
        .hex_field(&m.hex, |s: &mut Prefs| &mut s.appearance.mesh_edit.get_or_insert_with(|| Mesh { colors: vec![Hsv::new(0.0, 0.0, 0.2); MESH * MESH], cell: 0, hex: TextEdit::new("#333333") }).hex)
        .build(k);
    let value = mesh_value(&m.colors);
    let apply = Button::new("Use Gradient")
        .control_size(ControlSize::Small)
        .disabled(writing(s))
        .on_press(move |s: &mut Prefs| {
            s.appearance.mesh_edit = None;
            set(s, "background", &value, "a custom background");
        })
        .build(k)
        .key("appearance-mesh-use");
    let cancel = Button::new("Cancel").variant(ButtonVariant::Ghost).control_size(ControlSize::Small).on_press(|s: &mut Prefs| s.appearance.mesh_edit = None).build(k).key("appearance-mesh-cancel");
    let left = col().gap(10.0).child(el().w(132.0).h(82.0).radius(8.0).pixels(&preview)).child(grid);
    let actions = row().gap(8.0).child(cancel).child(apply);
    Some(
        col()
            .w_full()
            .px(14.0)
            .py(12.0)
            .gap(10.0)
            .key("appearance-mesh-editor")
            .child(text("Pick each corner and edge of a 3 by 3 gradient.").font(META).color(muted()))
            .child(row().gap(18.0).child(left).child(picker))
            .child(row().w_full().justify_end().child(actions)),
    )
}
