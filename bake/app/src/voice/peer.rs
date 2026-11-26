use std::sync::mpsc::Sender;

use feed::voice::{Peer, peer_allowed};
use rune::NativeView;
use wry::http::header::{AUTHORIZATION, HeaderValue};
use wry::http::{HeaderMap, Request};
use wry::{PageLoadEvent, PermissionKind, PermissionResponse, WebView, WebViewBuilder};

const BRIDGE: &str = "window.agenthailVoiceNotify = message => window.ipc.postMessage(JSON.stringify(message));";
pub const LOADED: &str = "loaded";

pub struct Web {
    view: WebView,
}

impl Web {
    pub fn build(slot: &NativeView, peer: &Peer, heard: Sender<(String, String)>) -> Result<Web, String> {
        let mut headers = HeaderMap::new();
        headers.insert(AUTHORIZATION, HeaderValue::from_str(&peer.authorization).map_err(|e| e.to_string())?);
        let (navigation, messages, loads) = (peer.clone(), peer.clone(), peer.clone());
        let from_page = heard.clone();
        let view = WebViewBuilder::new()
            .with_url_and_headers(peer.url.clone(), headers)
            .with_incognito(true)
            .with_autoplay(true)
            .with_transparent(true)
            .with_initialization_script(BRIDGE)
            .with_navigation_handler(move |url| peer_allowed(&url, &navigation))
            .with_permission_handler(|kind| match kind {
                PermissionKind::Microphone => PermissionResponse::Allow,
                _ => PermissionResponse::Deny,
            })
            .with_ipc_handler(move |request: Request<String>| {
                if !peer_allowed(&request.uri().to_string(), &messages) {
                    return;
                }
                let Ok(message) = serde_json::from_str::<serde_json::Value>(request.body()) else { return };
                let kind = message["type"].as_str().unwrap_or_default();
                if !kind.is_empty() && kind != LOADED {
                    let _ = from_page.send((kind.to_string(), message["value"].as_str().unwrap_or_default().to_string()));
                }
            })
            .with_on_page_load_handler(move |event, url| {
                if matches!(event, PageLoadEvent::Finished) && peer_allowed(&url, &loads) {
                    let _ = heard.send((LOADED.into(), String::new()));
                }
            })
            .build_as_child(slot)
            .map_err(|e| e.to_string())?;
        Ok(Web { view })
    }

    pub fn check(&self, heard: Sender<(String, String)>) {
        let asked = self.view.evaluate_script_with_callback("typeof window.agenthailVoice === 'object'", move |answer| {
            let message = if answer.trim() == "true" {
                ("ready".to_string(), String::new())
            } else {
                ("unavailable".to_string(), "The audio page didn't start. Check that Agenthail is running and up to date, then open this window again.".to_string())
            };
            let _ = heard.send(message);
        });
        if let Err(error) = asked {
            log_eval(&error.to_string());
        }
    }

    pub fn eval(&self, js: &str) {
        if let Err(error) = self.view.evaluate_script(js) {
            log_eval(&error.to_string());
        }
    }
}

fn log_eval(error: &str) {
    eprintln!("voice: the audio page refused a script: {error}");
}
