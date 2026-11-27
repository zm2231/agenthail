use rune::{El, row};
use rune_kit::{Button, ButtonVariant, ControlSize, Kit};

use crate::App;
use crate::over::decisions::{ReviewShown, shown_review_buttons};
use crate::over::{Need, NeedKind, Target, open_target};

#[derive(Hash)]
pub enum Acts {
    Review { id: String, shown: ReviewShown },
    OpenOnly,
    Pr(Option<String>),
    Primary { open: bool },
}

impl Acts {
    pub fn of(s: &App, n: &Need) -> Acts {
        let work = match &n.target {
            Target::Work(key) => s.ov.d.work.get(key).map(|w| (w, crate::over::listed_review(s, key))),
            _ => None,
        };
        match (n.kind, work) {
            (NeedKind::Check, Some((_, Some(d)))) => Acts::Review { id: d.id(), shown: ReviewShown::of(s, d, true) },
            (NeedKind::Check, _) => Acts::OpenOnly,
            (NeedKind::CiFailing, Some((w, d))) => {
                let fallback = feed::decisions::Decision::default();
                Acts::Pr(crate::over::work::pr_of(s, w, d.unwrap_or(&fallback), feed::derive::work::pr(w).as_deref()).map(|p| p.url))
            }
            _ => Acts::Primary { open: n.kind != NeedKind::CiFailing && !matches!(n.target, Target::Session(_)) },
        }
    }

    pub fn build(&self, n: &Need, compact: bool, k: &Kit) -> El {
        let size = if compact { ControlSize::Small } else { ControlSize::Regular };
        let r = row().gap(6.0).items_center();
        let opens = |variant| {
            let t = n.target.clone();
            Button::new("Open").variant(variant).control_size(size).on_press(move |s: &mut App| open_target(s, &t)).build(k)
        };
        match self {
            Acts::Review { id, shown } => {
                let Target::Work(key) = &n.target else { return r };
                let (ka, kc) = (key.clone(), key.clone());
                let approve = move |s: &mut App| crate::over::review_key(s, &ka, feed::review::Verdict::Approve);
                let changes = move |s: &mut App| crate::over::review_key(s, &kc, feed::review::Verdict::Reject);
                let key = format!("{}-{id}", if compact { "need-row" } else { "need-focus" });
                shown_review_buttons(shown, size, &key, r, k, approve, changes).child(opens(ButtonVariant::Ghost))
            }
            Acts::OpenOnly => r.child(opens(ButtonVariant::Contrast)),
            Acts::Pr(url) => {
                let (t, url) = (n.target.clone(), url.clone());
                r.child(
                    Button::new(n.kind.primary())
                        .variant(ButtonVariant::Contrast)
                        .control_size(size)
                        .on_press(move |s: &mut App| match &url {
                            Some(url) => crate::open_link(url),
                            None => open_target(s, &t),
                        })
                        .build(k),
                )
            }
            Acts::Primary { open } => {
                let t = n.target.clone();
                let r = r.child(Button::new(n.kind.primary()).variant(ButtonVariant::Contrast).control_size(size).on_press(move |s: &mut App| open_target(s, &t)).build(k));
                if *open { r.child(opens(ButtonVariant::Ghost)) } else { r }
            }
        }
    }
}
