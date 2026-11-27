use std::cell::RefCell;

use feed::derive::Target;
use feed::derive::menubar::{self, Link, MenuBar};
use rune::{AppHandle, MenuItem, Taskbar, Tray, TrayEvent, TrayIcon, TrayItem, TraySpec, WindowHandle};

use crate::App;
use crate::ui::keys::{self, MenuState};

const ICON: &[u8] = include_bytes!("../assets/menubar.png");
const WARNING: &[u8] = include_bytes!("../assets/menubar-warning.png");

#[derive(Default)]
pub struct Bar {
    tray: Option<Tray>,
    app: Option<AppHandle<App>>,
    shown: Option<(MenuBar, Link, Option<&'static str>)>,
    keys: Option<MenuState>,
    warning: bool,
}

impl Bar {
    pub fn is_shown(&self) -> bool {
        self.shown.is_some()
    }
}

thread_local! {
    static WINDOW: RefCell<Option<(WindowHandle, Option<u32>)>> = const { RefCell::new(None) };
}

pub fn install(app: rune::App<App>) -> rune::App<App> {
    let app = app.on_window(|window| WINDOW.with_borrow_mut(|w| *w = Some((window.clone(), None))));
    if rune::is_headless() {
        return app;
    }
    let handle = app.handle();
    let picks = handle.clone();
    let spec = match icon(ICON) {
        Ok(icon) => TraySpec::new("com.agenthail.app", "Agenthail", icon).tooltip("Agenthail").menu(tray_items(&MenuBar::default(), Link::Connecting, None)).menu_on_primary_click(true),
        Err(error) => {
            eprintln!("agenthail: no menu bar icon: {error}");
            return app;
        }
    };
    match Tray::new(spec, move |event| on_event(&picks, event)) {
        Ok(tray) => {
            let own = handle.clone();
            handle.update(move |s, _| {
                s.bar.tray = Some(tray);
                s.bar.app = Some(own);
            });
            app.keep_running()
        }
        Err(error) => {
            eprintln!("agenthail: no menu bar item: {error}");
            app
        }
    }
}

pub fn badge(s: &App) {
    WINDOW.with_borrow_mut(|w| {
        let Some((window, shown)) = w.as_mut() else { return };
        let (count, said) = menubar::badge(s.ov.needs.len());
        if *shown != Some(count) {
            window.set_taskbar(Taskbar::new().badge(count, said));
            *shown = Some(count);
        }
    });
}

pub fn warn(s: &mut App) {
    let link = Link::of(&s.status);
    if let Some(tray) = &s.bar.tray
        && link.warns() != s.bar.warning
        && show_link(tray, link)
    {
        s.bar.warning = link.warns();
    }
}

pub fn refresh(s: &mut App) {
    let bar = MenuBar::build(&s.ov.d, &s.ov.needs);
    let link = Link::of(&s.status);
    let note = crate::notify::menu_note(s);
    if s.bar.shown.as_ref().is_some_and(|(b, l, n)| *b == bar && *l == link && *n == note) {
        return;
    }
    if let Some(tray) = &s.bar.tray {
        tray.set_menu(tray_items(&bar, link, note));
    }
    s.bar.shown = Some((bar, link, note));
    s.bar.keys = None;
    sync_keys(s);
}

pub fn sync_keys(s: &mut App) {
    let state = keys::menu_state(s);
    if s.bar.keys == Some(state) {
        return;
    }
    if let (Some(app), Some((bar, ..))) = (&s.bar.app, &s.bar.shown) {
        let _ = app.set_menu(dock_items(bar).into_iter().chain([crate::trace::menu_item()]).chain(keys::menu_items(state)));
    }
    s.bar.keys = Some(state);
}

fn show_link(tray: &Tray, link: Link) -> bool {
    let (art, tip, said) = if link.warns() { (WARNING, link.label(), "Agenthail unavailable") } else { (ICON, "Agenthail", "Agenthail") };
    match icon(art).and_then(|i| tray.set_icon(i).map_err(|e| e.to_string())) {
        Ok(()) => {
            tray.set_tooltip(tip);
            tray.set_accessibility_label(said);
            true
        }
        Err(error) => {
            eprintln!("agenthail: menu bar icon not changed: {error}");
            false
        }
    }
}

fn tray_items(bar: &MenuBar, link: Link, note: Option<&str>) -> Vec<TrayItem> {
    let mut items = vec![TrayItem::action("status", link.label()).enabled(false)];
    if let Some(note) = note {
        items.extend([TrayItem::action("notifications-off", note).enabled(false), TrayItem::action("notification-settings", "Turn On Notifications…")]);
    }
    items.push(TrayItem::Separator);
    for (i, (title, rows)) in bar.sections().into_iter().enumerate() {
        items.push(TrayItem::action(format!("section-{i}"), title).enabled(false));
        items.extend(rows.iter().map(|r| TrayItem::action(menubar::item_id(&r.target), r.label.clone())));
        items.push(TrayItem::Separator);
    }
    items.extend([
        TrayItem::action("new-session", "New Session"),
        TrayItem::action("open", "Open Agenthail"),
        TrayItem::action("settings", "Settings…"),
        TrayItem::Separator,
        TrayItem::action("quit", "Quit Agenthail"),
    ]);
    items
}

pub fn with_menus(app: rune::App<App>) -> rune::App<App> {
    keys::menu_items(MenuState::default()).into_iter().fold(app.menu_item(settings_item()).menu_item(crate::trace::menu_item()), rune::App::menu_item)
}

fn settings_item() -> MenuItem<App> {
    let shortcut = "cmd+comma".parse().expect("the Settings shortcut parses");
    MenuItem::new("Settings…", crate::settings::open_from_menu).id("settings").key_equivalent(shortcut)
}

fn dock_items(bar: &MenuBar) -> Vec<MenuItem<App>> {
    let mut items = vec![settings_item()];
    for (i, (title, rows)) in bar.sections().into_iter().enumerate() {
        if i > 0 {
            items.push(MenuItem::separator().dock());
        }
        items.push(MenuItem::with_action(title, |_: &mut App, _| {}).id(format!("dock-section-{i}")).enabled(false).dock());
        for r in rows {
            let target = r.target.clone();
            items.push(MenuItem::with_action(r.label.clone(), move |s: &mut App, _| open_target(s, &target)).id(format!("dock-{}", menubar::item_id(&r.target))).dock());
        }
    }
    items
}

fn open_target(s: &mut App, target: &Target) {
    front(s);
    crate::over::open_target(s, target);
}

pub fn front(s: &App) {
    if let Some(app) = &s.bar.app {
        app.open();
    } else if let Some((window, _)) = WINDOW.with_borrow(|w| w.clone()) {
        window.focus();
    }
}

fn on_event(app: &AppHandle<App>, event: TrayEvent) {
    match event {
        TrayEvent::Activate { token: Some(token) } => app.activate(token),
        TrayEvent::Activate { token: None } => app.open(),
        TrayEvent::Menu(id) => match id.as_str() {
            "open" => app.open(),
            "quit" => app.quit(),
            "settings" => crate::settings::open(),
            "notification-settings" => crate::open_settings(feed::notify::NOTIFICATION_SETTINGS),
            "new-session" => {
                app.open();
                app.update(|s, tx| {
                    let prefill = crate::ui::new_session::prefill_for(s);
                    crate::ui::new_session::open(s, prefill, tx);
                });
            }
            _ => {
                if let Some(target) = menubar::target_of(&id) {
                    app.open();
                    app.update(move |s, _| crate::over::open_target(s, &target));
                }
            }
        },
        _ => {}
    }
}

fn icon(art: &[u8]) -> Result<TrayIcon, String> {
    let mut decoder = png::Decoder::new(std::io::Cursor::new(art));
    decoder.set_transformations(png::Transformations::EXPAND | png::Transformations::STRIP_16);
    let mut reader = decoder.read_info().map_err(|e| e.to_string())?;
    let (width, height) = (reader.info().width, reader.info().height);
    let mut buf = vec![0; width as usize * height as usize * 4];
    let frame = reader.next_frame(&mut buf).map_err(|e| e.to_string())?;
    let pixels = &buf[..frame.buffer_size()];
    let rgba: Vec<u8> = match frame.color_type {
        png::ColorType::Rgba => pixels.to_vec(),
        png::ColorType::Rgb => pixels.chunks_exact(3).flat_map(|p| [p[0], p[1], p[2], 255]).collect(),
        png::ColorType::GrayscaleAlpha => pixels.chunks_exact(2).flat_map(|p| [p[0], p[0], p[0], p[1]]).collect(),
        png::ColorType::Grayscale => pixels.iter().flat_map(|&g| [g, g, g, 255]).collect(),
        other => return Err(format!("unexpected colour type {other:?}")),
    };
    Ok(TrayIcon::rgba(width, height, rgba).template(true))
}
