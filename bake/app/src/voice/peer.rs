use std::sync::mpsc::Sender;

use feed::voice::{Peer, peer_allowed};

use super::Heard;
use rune::NativeView;
use wry::http::header::{AUTHORIZATION, HeaderValue};
use wry::http::{HeaderMap, Request};
use wry::{PageLoadEvent, PermissionKind, PermissionResponse, WebView, WebViewBuilder};

const BRIDGE: &str = "window.agenthailVoiceNotify = message => window.ipc.postMessage(JSON.stringify({...message, call: window.agenthailCall || \"\"}));";
pub const LOADED: &str = "loaded";
pub const CHECKED: &str = "checked";

pub struct Web {
    view: WebView,
}

impl Web {
    pub fn build(slot: &NativeView, peer: &Peer, heard: Sender<Heard>) -> Result<Web, String> {
        let headers = authorized(peer)?;
        let (navigation, messages, loads) = (peer.clone(), peer.clone(), peer.clone());
        let from_page = heard.clone();
        let view = WebViewBuilder::new()
            .with_url_and_headers(peer.url.clone(), headers)
            .with_incognito(true)
            .with_autoplay(true)
            .with_initialization_script(BRIDGE)
            .with_navigation_handler(move |url| {
                let allowed = peer_allowed(&url, &navigation);
                if !allowed {
                    eprintln!("voice: the audio page tried to leave for {}", without_query(&url));
                }
                allowed
            })
            .with_permission_handler(|kind| match kind {
                PermissionKind::Microphone => PermissionResponse::Allow,
                _ => PermissionResponse::Deny,
            })
            .with_ipc_handler(move |request: Request<String>| {
                let from = request.uri().to_string();
                if !peer_allowed(&from, &messages) {
                    eprintln!("voice: ignored a message from {}", without_query(&from));
                    return;
                }
                let Ok(message) = serde_json::from_str::<serde_json::Value>(request.body()) else { return };
                let kind = message["type"].as_str().unwrap_or_default();
                if !kind.is_empty() && kind != LOADED {
                    let value = message["value"].as_str().unwrap_or_default().to_string();
                    let _ = from_page.send((message["call"].as_str().unwrap_or_default().to_string(), kind.to_string(), value));
                }
            })
            .with_on_page_load_handler(move |event, url| {
                let shown = without_query(&url);
                match event {
                    PageLoadEvent::Started => eprintln!("voice: loading {shown}"),
                    PageLoadEvent::Finished if peer_allowed(&url, &loads) => {
                        eprintln!("voice: loaded {shown}");
                        let _ = heard.send((String::new(), LOADED.into(), String::new()));
                    }
                    PageLoadEvent::Finished => {
                        let _ = heard.send((String::new(), "unavailable".into(), format!("The audio page opened at an unexpected address: {shown}")));
                    }
                }
            })
            .build_as_child(slot)
            .map_err(|e| e.to_string())?;
        Ok(Web { view })
    }

    pub fn check(&self, heard: Sender<Heard>, load: u64) {
        let asked = self.view.evaluate_script_with_callback("typeof window.agenthailVoice === 'object'", move |answer| {
            eprintln!("voice: readiness check answered {}", answer.trim());
            let _ = heard.send((load.to_string(), CHECKED.into(), (answer.trim() == "true").to_string()));
        });
        if let Err(error) = asked {
            log_eval(&error.to_string());
        }
    }

    pub fn reload(&self, peer: &Peer) {
        let loaded = authorized(peer).and_then(|headers| self.view.load_url_with_headers(&peer.url, headers).map_err(|e| e.to_string()));
        if let Err(error) = loaded {
            eprintln!("voice: the audio page couldn't be reloaded: {error}");
        }
    }

    pub fn eval(&self, js: &str) {
        if let Err(error) = self.view.evaluate_script(js) {
            log_eval(&error.to_string());
        }
    }
}

fn authorized(peer: &Peer) -> Result<HeaderMap, String> {
    let mut headers = HeaderMap::new();
    headers.insert(AUTHORIZATION, HeaderValue::from_str(&peer.authorization).map_err(|e| e.to_string())?);
    Ok(headers)
}

fn without_query(url: &str) -> &str {
    url.split(['?', '#']).next().unwrap_or(url)
}

fn log_eval(error: &str) {
    eprintln!("voice: the audio page refused a script: {error}");
}
