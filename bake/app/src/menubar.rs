use std::cell::RefCell;

use feed::derive::Target;
use feed::derive::menubar::{self, MenuBar};
use rune::{AppHandle, MenuItem, Taskbar, Tray, TrayEvent, TrayIcon, TrayItem, TraySpec, WindowHandle};

use crate::App;

const ICON: &[u8] = include_bytes!("../assets/menubar.png");

#[derive(Default)]
pub struct Bar {
    tray: Option<Tray>,
    app: Option<AppHandle<App>>,
    shown: Option<(MenuBar, &'static str)>,
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
    let spec = match icon() {
        Ok(icon) => TraySpec::new("com.agenthail.app", "Agenthail", icon).tooltip("Agenthail").menu(tray_items(&MenuBar::default(), "Connecting…")),
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

pub fn refresh(s: &mut App) {
    let bar = MenuBar::build(&s.ov.d, &s.ov.needs);
    let status = status(&s.status);
    if s.bar.shown.as_ref().is_some_and(|(b, st)| *b == bar && *st == status) {
        return;
    }
    if let Some(tray) = &s.bar.tray {
        tray.set_menu(tray_items(&bar, status));
    }
    if let Some(app) = &s.bar.app {
        let _ = app.set_menu(dock_items(&bar));
    }
    s.bar.shown = Some((bar, status));
}

fn status(status: &str) -> &'static str {
    match status {
        "live" => "Connected",
        "connecting" => "Connecting…",
        "fixture" | "fixtures" => "Showing a recording",
        _ => "Agenthail isn't running",
    }
}

fn tray_items(bar: &MenuBar, status: &str) -> Vec<TrayItem> {
    let mut items = vec![TrayItem::action("status", status).enabled(false), TrayItem::Separator];
    for (i, (title, rows)) in bar.sections().into_iter().enumerate() {
        items.push(TrayItem::action(format!("section-{i}"), title).enabled(false));
        items.extend(rows.iter().map(|r| TrayItem::action(menubar::item_id(&r.target), r.label.clone())));
        items.push(TrayItem::Separator);
    }
    items.extend([
        TrayItem::action("new-session", "New Session"),
        TrayItem::action("open", "Open Agenthail"),
        TrayItem::action("settings", "Settings…").enabled(false),
        TrayItem::Separator,
        TrayItem::action("quit", "Quit Agenthail"),
    ]);
    items
}

fn dock_items(bar: &MenuBar) -> Vec<MenuItem<App>> {
    let mut items = Vec::new();
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
    if let Some(app) = &s.bar.app {
        app.open();
    }
    crate::over::open_target(s, target);
}

fn on_event(app: &AppHandle<App>, event: TrayEvent) {
    match event {
        TrayEvent::Activate { token: Some(token) } => app.activate(token),
        TrayEvent::Activate { token: None } => app.open(),
        TrayEvent::Menu(id) => match id.as_str() {
            "open" => app.open(),
            "quit" => app.quit(),
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

fn icon() -> Result<TrayIcon, String> {
    let mut decoder = png::Decoder::new(std::io::Cursor::new(ICON));
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
