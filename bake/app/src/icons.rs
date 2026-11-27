use rune::Icon;

macro_rules! lucide {
    ($id:ident, $name:literal, $body:literal) => {
        pub static $id: Icon = Icon {
            name: $name,
            view: 24.0,
            body: concat!(
                r#"<g fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">"#,
                $body,
                "</g>"
            ),
        };
    };
}

lucide!(BRANCH, "ah-branch", r#"<circle cx="6" cy="5" r="2.2"/><circle cx="6" cy="19" r="2.2"/><circle cx="18" cy="7" r="2.2"/><path d="M6 7.2v9.6"/><path d="M18 9.2c0 5-6 4-11.2 8"/>"#);
lucide!(SERVER, "ah-server", r#"<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>"#);
lucide!(FOLDER, "ah-folder", r#"<path d="M3 7.5A2.5 2.5 0 0 1 5.5 5h3.8l2 2.2h7.2A2.5 2.5 0 0 1 21 9.7v7.8a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5z"/>"#);
lucide!(TERMINAL, "ah-terminal", r#"<path d="m5 8 4 4-4 4"/><path d="M12 17h7"/>"#);
lucide!(CHEVRON_RIGHT, "ah-chevron-right", r#"<path d="m9 6 6 6-6 6"/>"#);
lucide!(CHEVRON_LEFT, "ah-chevron-left", r#"<path d="m15 6-6 6 6 6"/>"#);
lucide!(CHEVRON_DOWN, "ah-chevron-down", r#"<path d="m6 9 6 6 6-6"/>"#);
lucide!(ARROW_UP, "ah-arrow-up", r#"<path d="M12 19V5"/><path d="m5 12 7-7 7 7"/>"#);
lucide!(SPARKLES, "ah-sparkles", r#"<path d="M12 3.5 13.8 9l5.7 1.8-5.7 1.8L12 18.4l-1.8-5.8-5.7-1.8L10.2 9z"/><path d="M19 3v3M17.5 4.5h3"/>"#);
lucide!(HAND, "ah-hand", r#"<path d="M8 12V5.5a1.5 1.5 0 0 1 3 0V11"/><path d="M11 10V4a1.5 1.5 0 0 1 3 0v6"/><path d="M14 10V5.5a1.5 1.5 0 0 1 3 0V13"/><path d="M8 12V9.5a1.5 1.5 0 0 0-3 0V14a7 7 0 0 0 7 7h1a6 6 0 0 0 6-6v-4.5a1.5 1.5 0 0 0-3 0"/>"#);
lucide!(CLOCK, "ah-clock", r#"<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>"#);
lucide!(CHECK_CIRCLE, "ah-check-circle", r#"<circle cx="12" cy="12" r="8.5"/><path d="m8.5 12.3 2.4 2.4 4.6-5"/>"#);
lucide!(INBOX, "ah-inbox", r#"<path d="M3.5 13h4.5l1.5 2.5h5L16 13h4.5"/><path d="M5.6 6.4 3.5 13v4.5A2.5 2.5 0 0 0 6 20h12a2.5 2.5 0 0 0 2.5-2.5V13l-2.1-6.6A2 2 0 0 0 16.5 5h-9a2 2 0 0 0-1.9 1.4z"/>"#);
lucide!(BOLT, "ah-bolt", r#"<path d="M13 2.5 4.5 13.5H12l-1 8 8.5-11H12z"/>"#);
lucide!(MESSAGE, "ah-message", r#"<path d="M20 14.5a2 2 0 0 1-2 2H8l-4 3.5V6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2z"/>"#);
lucide!(LAYERS, "ah-layers", r#"<path d="m12 3 9 4.8-9 4.8-9-4.8z"/><path d="m3 12.2 9 4.8 9-4.8"/><path d="m3 16.6 9 4.8 9-4.8"/>"#);
lucide!(TARGET, "ah-target", r#"<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="4.5"/><circle cx="12" cy="12" r="1"/>"#);
lucide!(PULL, "ah-pull", r#"<circle cx="6" cy="6" r="2.2"/><circle cx="6" cy="18" r="2.2"/><circle cx="18" cy="18" r="2.2"/><path d="M6 8.2v7.6"/><path d="M18 15.8V10a3 3 0 0 0-3-3h-4"/><path d="m13 4.5-2.5 2.5 2.5 2.5"/>"#);
lucide!(ARROW_RIGHT, "ah-arrow-right", r#"<path d="M5 12h14"/><path d="m13 6 6 6-6 6"/>"#);
lucide!(ACTIVITY, "ah-activity", r#"<path d="M3 12h4l2.5-6 5 12 2.5-6h4"/>"#);
lucide!(LIST, "ah-list", r#"<path d="M9 6h11M9 12h11M9 18h11"/><path d="M4.5 6h.01M4.5 12h.01M4.5 18h.01"/>"#);
lucide!(CORNER, "ah-corner", r#"<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5A5.5 5.5 0 0 1 20 14.5 5.5 5.5 0 0 1 14.5 20H11"/>"#);
lucide!(MORE, "ah-more", r#"<circle cx="5.5" cy="12" r="1.2"/><circle cx="12" cy="12" r="1.2"/><circle cx="18.5" cy="12" r="1.2"/>"#);
lucide!(PIN, "ah-pin", r#"<path d="M12 17v4.5"/><path d="M8.5 3.5h7l-1 6 3.5 3.5v2.5h-12V13l3.5-3.5z"/>"#);
lucide!(SHRINK, "ah-shrink", r#"<path d="M4 14h6v6M20 10h-6V4M14 10l7-7M3 21l7-7"/>"#);
lucide!(SELECTOR, "ah-selector", r#"<path d="m8 9 4-4 4 4"/><path d="m8 15 4 4 4-4"/>"#);
lucide!(STOP, "ah-stop", r#"<rect x="6.5" y="6.5" width="11" height="11" rx="2" fill="currentColor"/>"#);
lucide!(ROTATE, "ah-rotate", r#"<path d="M4 12a8 8 0 1 0 2.4-5.7L4 8.5"/><path d="M4 4v4.5h4.5"/>"#);
lucide!(ROTATE_CW, "ah-rotate-cw", r#"<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>"#);
lucide!(COPY, "ah-copy", r#"<rect x="8.5" y="8.5" width="12" height="12" rx="2"/><path d="M15.5 8.5V5.5a2 2 0 0 0-2-2h-8a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h3"/>"#);
lucide!(PENCIL, "ah-pencil", r#"<path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7.5 18.5 3.5 19.5l1-4z"/><path d="m14.5 5.5 3 3"/>"#);
lucide!(TRASH, "ah-trash", r#"<path d="M4 7h16"/><path d="M10 11v6M14 11v6"/><path d="M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/><path d="M9 7V4.5h6V7"/>"#);
lucide!(GAUGE, "ah-gauge", r#"<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>"#);
lucide!(PHONE, "ah-phone", r#"<path d="M5 4h3.5l1.8 4.5-2.3 1.4a11 11 0 0 0 6.1 6.1l1.4-2.3L20 15.5V19a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2"/>"#);
lucide!(PHONE_OFF, "ah-phone-off", r#"<path d="M10.7 13.3a11 11 0 0 0 3.4 2.7l1.4-2.3L20 15.5V19a2 2 0 0 1-2 2 16 16 0 0 1-9.6-4.4"/><path d="M5.6 13.4A16 16 0 0 1 3 6a2 2 0 0 1 2-2h3.5l1.8 4.5-2.3 1.4"/><path d="M21 3 3 21"/>"#);
lucide!(MIC, "ah-mic", r#"<rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5.5 11a6.5 6.5 0 0 0 13 0"/><path d="M12 17.5V21"/>"#);
lucide!(MIC_OFF, "ah-mic-off", r#"<path d="M15 9.5V6a3 3 0 0 0-5.7-1.3"/><path d="M9 9v2a3 3 0 0 0 5 2.2"/><path d="M18.5 11a6.5 6.5 0 0 1-1 3.4M5.5 11a6.5 6.5 0 0 0 10.4 5.2"/><path d="M12 17.5V21"/><path d="M3 3l18 18"/>"#);
lucide!(PAPERCLIP, "ah-paperclip", r#"<path d="m21 11.5-8.6 8.6a5.5 5.5 0 0 1-7.8-7.8l8.6-8.6a3.7 3.7 0 0 1 5.2 5.2l-8.6 8.6a1.8 1.8 0 0 1-2.6-2.6l8-8"/>"#);
lucide!(CLOSE, "ah-close", r#"<path d="M18 6 6 18"/><path d="m6 6 12 12"/>"#);
lucide!(FILE, "ah-file", r#"<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>"#);
lucide!(FILE_TEXT, "ah-file-text", r#"<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/><path d="M9 13h6M9 17h6"/>"#);
lucide!(IMAGE, "ah-image", r#"<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.1-3.1a2 2 0 0 0-2.8 0L6 21"/>"#);
lucide!(FILM, "ah-film", r#"<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M7 3v18M17 3v18M3 7.5h4M3 12h18M3 16.5h4M17 7.5h4M17 16.5h4"/>"#);
lucide!(AUDIO, "ah-audio", r#"<path d="M2 13a2 2 0 0 0 2-2V7a2 2 0 0 1 4 0v13a2 2 0 0 0 4 0V4a2 2 0 0 1 4 0v13a2 2 0 0 0 4 0v-4a2 2 0 0 1 2-2"/>"#);
lucide!(ARCHIVE, "ah-archive", r#"<rect x="3" y="3" width="18" height="5" rx="1"/><path d="M5 8v11a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>"#);
lucide!(CHEVRON_UP, "ah-chevron-up", r#"<path d="m18 15-6-6-6 6"/>"#);
lucide!(COG, "ah-cog", r#"<path d="M12.2 2h-.4a2 2 0 0 0-2 2v.2a2 2 0 0 1-1 1.7l-.4.3a2 2 0 0 1-2 0l-.2-.1a2 2 0 0 0-2.7.7l-.2.4a2 2 0 0 0 .7 2.7l.2.1a2 2 0 0 1 1 1.7v.5a2 2 0 0 1-1 1.7l-.2.1a2 2 0 0 0-.7 2.7l.2.4a2 2 0 0 0 2.7.7l.2-.1a2 2 0 0 1 2 0l.4.3a2 2 0 0 1 1 1.7v.2a2 2 0 0 0 2 2h.4a2 2 0 0 0 2-2v-.2a2 2 0 0 1 1-1.7l.4-.3a2 2 0 0 1 2 0l.2.1a2 2 0 0 0 2.7-.7l.2-.4a2 2 0 0 0-.7-2.7l-.2-.1a2 2 0 0 1-1-1.7v-.5a2 2 0 0 1 1-1.7l.2-.1a2 2 0 0 0 .7-2.7l-.2-.4a2 2 0 0 0-2.7-.7l-.2.1a2 2 0 0 1-2 0l-.4-.3a2 2 0 0 1-1-1.7V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>"#);
lucide!(USERS, "ah-users", r#"<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.9"/><path d="M16 3.1a4 4 0 0 1 0 7.8"/>"#);
lucide!(SHARE, "ah-share", r#"<path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/><path d="m16 6-4-4-4 4"/><path d="M12 2v13"/>"#);
