use std::time::{Duration, Instant};

use crate::shortcuts::Stored;

const WINDOW_SWITCH: Duration = Duration::from_millis(300);

#[derive(Debug, Default)]
pub struct Return {
    left: Option<Instant>,
}

impl Return {
    pub fn focus(&mut self, focused: bool, now: Instant) -> bool {
        if !focused {
            self.left = Some(now);
            return false;
        }
        self.left.take().is_some_and(|left| now.saturating_duration_since(left) >= WINDOW_SWITCH)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Ask {
    Want,
    Choose,
    Regain,
}

pub fn latest(asks: impl IntoIterator<Item = (Ask, Option<Stored>)>) -> Option<(Ask, Option<Stored>)> {
    asks.into_iter().reduce(|(was, _), (ask, shortcut)| {
        let kind = if was == Ask::Choose || ask == Ask::Choose {
            Ask::Choose
        } else if was == Ask::Regain || ask == Ask::Regain {
            Ask::Regain
        } else {
            Ask::Want
        };
        (kind, shortcut)
    })
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Refusal {
    Taken(Option<String>),
    Denied,
    Unsupported,
    Invalid,
    Failed(String),
}

pub trait Registrar {
    type Id: Copy;
    fn register(&mut self, shortcut: Stored) -> Result<Self::Id, Refusal>;
    fn unregister(&mut self, id: Self::Id);
}

#[derive(Debug)]
pub struct Hotkey<I> {
    held: Option<I>,
    tried: Option<Stored>,
    refused: Option<Refusal>,
}

impl<I> Default for Hotkey<I> {
    fn default() -> Self {
        Hotkey { held: None, tried: None, refused: None }
    }
}

impl<I: Copy> Hotkey<I> {
    pub fn ask<R: Registrar<Id = I>>(&mut self, registrar: &mut R, ask: Ask, shortcut: Option<Stored>) -> bool {
        match ask {
            Ask::Want => self.want(registrar, shortcut),
            Ask::Choose => self.choose(registrar, shortcut),
            Ask::Regain => self.regain(registrar, shortcut),
        }
    }

    pub fn want<R: Registrar<Id = I>>(&mut self, registrar: &mut R, shortcut: Option<Stored>) -> bool {
        if shortcut == self.tried {
            return false;
        }
        self.attempt(registrar, shortcut);
        true
    }

    pub fn choose<R: Registrar<Id = I>>(&mut self, registrar: &mut R, shortcut: Option<Stored>) -> bool {
        if shortcut == self.tried && self.refused.is_none() {
            return false;
        }
        self.attempt(registrar, shortcut);
        true
    }

    pub fn regain<R: Registrar<Id = I>>(&mut self, registrar: &mut R, shortcut: Option<Stored>) -> bool {
        if shortcut == self.tried && !matches!(self.refused, Some(Refusal::Taken(_))) {
            return false;
        }
        self.attempt(registrar, shortcut);
        true
    }

    fn attempt<R: Registrar<Id = I>>(&mut self, registrar: &mut R, shortcut: Option<Stored>) {
        if let Some(id) = self.held.take() {
            registrar.unregister(id);
        }
        self.tried = shortcut;
        self.refused = None;
        let Some(shortcut) = shortcut else { return };
        match registrar.register(shortcut) {
            Ok(id) => self.held = Some(id),
            Err(refusal) => self.refused = Some(refusal),
        }
    }

    pub fn held(&self) -> bool {
        self.held.is_some()
    }

    pub fn refused(&self) -> Option<&Refusal> {
        self.refused.as_ref()
    }
}

pub fn note(refusal: &Refusal, keys: &str) -> String {
    match refusal {
        Refusal::Taken(Some(owner)) => format!("{keys} is already used by {owner}. Agenthail tries again when you change it or come back to Agenthail."),
        Refusal::Taken(None) => format!("{keys} is already used by another app. Agenthail tries again when you change it or come back to Agenthail."),
        Refusal::Denied => format!("{keys} wasn't allowed from other apps. Choose it again to ask once more."),
        Refusal::Unsupported => "This desktop doesn't offer shortcuts that work from other apps.".to_string(),
        Refusal::Invalid => format!("{keys} can't be used from other apps. Add ⌘, ⌃ or ⌥."),
        Refusal::Failed(why) => format!("{keys} couldn't be set up: {why}."),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Default)]
    struct Fake {
        next: u32,
        live: Vec<(u32, Stored)>,
        taken: Vec<(Stored, Option<String>)>,
        refuse: Option<Refusal>,
        calls: usize,
    }

    impl Registrar for Fake {
        type Id = u32;

        fn register(&mut self, shortcut: Stored) -> Result<u32, Refusal> {
            self.calls += 1;
            if let Some(refusal) = self.refuse.clone() {
                return Err(refusal);
            }
            if let Some((_, owner)) = self.taken.iter().find(|(s, _)| *s == shortcut) {
                return Err(Refusal::Taken(owner.clone()));
            }
            self.next += 1;
            self.live.push((self.next, shortcut));
            Ok(self.next)
        }

        fn unregister(&mut self, id: u32) {
            self.live.retain(|(i, _)| *i != id);
        }
    }

    fn s(spec: &str) -> Stored {
        Stored::parse(spec).unwrap()
    }

    #[test]
    fn moving_between_agenthails_own_windows_is_not_coming_back() {
        let t = Instant::now();
        let mut r = Return::default();
        assert!(!r.focus(true, t), "the first focus at launch isn't a return");
        assert!(!r.focus(false, t + Duration::from_secs(1)));
        assert!(!r.focus(true, t + Duration::from_millis(1005)), "another of its windows took focus straight away");
        assert!(!r.focus(false, t + Duration::from_secs(2)));
        assert!(r.focus(true, t + Duration::from_secs(9)), "coming back from another app");
        assert!(!r.focus(true, t + Duration::from_secs(10)), "a focus without a blur is not a return");
    }

    #[test]
    fn asks_waiting_behind_a_slow_one_settle_on_the_newest_binding() {
        let (a, b) = (Some(s("alt+mod+k")), Some(s("ctrl+alt+space")));
        assert_eq!(latest([]), None);
        assert_eq!(latest([(Ask::Choose, b), (Ask::Want, None)]), Some((Ask::Choose, None)));
        assert_eq!(latest([(Ask::Want, a), (Ask::Regain, a)]), Some((Ask::Regain, a)));
        assert_eq!(latest([(Ask::Want, a), (Ask::Want, b)]), Some((Ask::Want, b)));

        let mut fake = Fake::default();
        let mut hk = Hotkey::default();
        hk.ask(&mut fake, Ask::Choose, a);
        let (ask, shortcut) = latest([(Ask::Choose, b), (Ask::Want, None)]).unwrap();
        hk.ask(&mut fake, ask, shortcut);
        assert_eq!(fake.calls, 1, "the passed-over choice is never registered");
        assert!(fake.live.is_empty());
        assert!(!hk.held());
    }

    #[test]
    fn an_unset_shortcut_registers_nothing() {
        let (mut fake, mut hk) = (Fake::default(), Hotkey::default());
        assert!(!hk.want(&mut fake, None));
        assert_eq!(fake.calls, 0);
        assert!(!hk.held());
        assert_eq!(hk.refused(), None);
    }

    #[test]
    fn a_rebinding_releases_the_old_shortcut_before_taking_the_new_one() {
        let (mut fake, mut hk) = (Fake::default(), Hotkey::default());
        assert!(hk.want(&mut fake, Some(s("alt+mod+k"))));
        assert_eq!(fake.live, vec![(1, s("alt+mod+k"))]);
        assert!(!hk.want(&mut fake, Some(s("alt+mod+k"))), "the same binding does nothing");
        assert_eq!(fake.calls, 1);
        assert!(hk.want(&mut fake, Some(s("ctrl+alt+space"))));
        assert_eq!(fake.live, vec![(2, s("ctrl+alt+space"))]);
        assert!(hk.want(&mut fake, None));
        assert!(fake.live.is_empty(), "clearing the binding releases it");
        assert!(!hk.held());
    }

    #[test]
    fn a_taken_shortcut_names_its_owner_and_retries_only_when_asked() {
        let mut fake = Fake { taken: vec![(s("alt+mod+k"), None)], ..Fake::default() };
        let mut hk = Hotkey::default();
        hk.want(&mut fake, Some(s("alt+mod+k")));
        assert_eq!(hk.refused(), Some(&Refusal::Taken(None)));
        assert!(!hk.want(&mut fake, Some(s("alt+mod+k"))), "no retry without a change");
        assert_eq!(fake.calls, 1);
        assert!(hk.regain(&mut fake, Some(s("alt+mod+k"))), "coming back retries a taken shortcut");
        assert_eq!(fake.calls, 2);
        fake.taken.clear();
        assert!(hk.regain(&mut fake, Some(s("alt+mod+k"))));
        assert!(hk.held());
        assert_eq!(hk.refused(), None);
        assert!(!hk.regain(&mut fake, Some(s("alt+mod+k"))), "a held shortcut isn't retried");
        assert!(!hk.choose(&mut fake, Some(s("alt+mod+k"))), "choosing the held shortcut again does nothing");
        assert_eq!(fake.calls, 3);

        let mut fake = Fake { taken: vec![(s("mod+space"), Some("Spotlight".into()))], ..Fake::default() };
        let mut hk = Hotkey::default();
        hk.want(&mut fake, Some(s("mod+space")));
        assert_eq!(note(hk.refused().unwrap(), "⌘Space"), "⌘Space is already used by Spotlight. Agenthail tries again when you change it or come back to Agenthail.");
        assert!(hk.want(&mut fake, Some(s("alt+mod+k"))), "a rebinding moves off the taken shortcut");
        assert!(hk.held());
    }

    #[test]
    fn coming_back_to_a_binding_changed_elsewhere_tries_it_once() {
        let mut fake = Fake { taken: vec![(s("ctrl+alt+space"), None)], ..Fake::default() };
        let mut hk = Hotkey::default();
        hk.want(&mut fake, Some(s("alt+mod+k")));
        assert!(hk.regain(&mut fake, Some(s("ctrl+alt+space"))));
        assert_eq!(fake.calls, 2, "one attempt for the new binding, no second retry in the same return");
        assert!(fake.live.is_empty(), "the old shortcut was released");
        assert_eq!(hk.refused(), Some(&Refusal::Taken(None)));
    }

    #[test]
    fn a_refusal_other_than_taken_waits_for_a_choice() {
        for refusal in [Refusal::Unsupported, Refusal::Denied, Refusal::Invalid, Refusal::Failed("native status -1".into())] {
            let mut fake = Fake { refuse: Some(refusal.clone()), ..Fake::default() };
            let mut hk = Hotkey::default();
            hk.want(&mut fake, Some(s("alt+mod+k")));
            assert_eq!(hk.refused(), Some(&refusal));
            assert!(!hk.regain(&mut fake, Some(s("alt+mod+k"))));
            assert_eq!(fake.calls, 1);
            fake.refuse = None;
            assert!(hk.choose(&mut fake, Some(s("alt+mod+k"))), "choosing it again asks again");
            assert!(hk.held());
            assert!(!note(&refusal, "⌥⌘K").is_empty());
        }
        assert_eq!(note(&Refusal::Unsupported, "⌥⌘K"), "This desktop doesn't offer shortcuts that work from other apps.");
    }
}
