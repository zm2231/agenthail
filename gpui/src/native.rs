use global_hotkey::hotkey::{Code, HotKey, Modifiers};
use global_hotkey::{GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState};
use objc2::MainThreadMarker;
use objc2::rc::Retained;
use objc2_app_kit::{NSApplication, NSStatusBar, NSStatusItem, NSVariableStatusItemLength};
use objc2_foundation::NSString;

use crate::probe;

pub struct Native {
    status: Retained<NSStatusItem>,
    _hotkeys: Option<GlobalHotKeyManager>,
    shown: Option<usize>,
}

impl Native {
    pub fn install() -> Option<(Self, async_channel::Receiver<()>)> {
        MainThreadMarker::new()?;
        let status = NSStatusBar::systemStatusBar().statusItemWithLength(NSVariableStatusItemLength);
        let hotkeys = GlobalHotKeyManager::new().ok().and_then(|manager| {
            let key = HotKey::new(Some(Modifiers::CONTROL | Modifiers::ALT | Modifiers::SUPER), Code::KeyH);
            let registered = manager.register(key);
            probe::record("hotkey-register", &format!("{registered:?}"));
            registered.ok().map(|_| manager)
        });
        let (tx, rx) = async_channel::unbounded();
        std::thread::spawn(move || {
            while let Ok(event) = GlobalHotKeyEvent::receiver().recv() {
                if event.state == HotKeyState::Pressed && tx.send_blocking(()).is_err() {
                    break;
                }
            }
        });
        let mut native = Self { status, _hotkeys: hotkeys, shown: None };
        native.set_needs(0);
        Some((native, rx))
    }

    pub fn set_needs(&mut self, count: usize) {
        if self.shown == Some(count) {
            return;
        }
        let Some(mtm) = MainThreadMarker::new() else { return };
        self.shown = Some(count);
        let label = (count > 0).then(|| NSString::from_str(&count.to_string()));
        let tile = NSApplication::sharedApplication(mtm).dockTile();
        tile.setBadgeLabel(None);
        tile.setBadgeLabel(label.as_deref());
        if let Some(button) = self.status.button(mtm) {
            button.setTitle(&NSString::from_str(&format!("Agenthail {count}")));
        }
        let read = NSApplication::sharedApplication(mtm).dockTile().badgeLabel().map(|s| s.to_string());
        probe::record("badge", &format!("{count}\tread={read:?}"));
    }

    pub fn refresh(&mut self, count: usize) {
        self.shown = None;
        self.set_needs(count);
    }
}
