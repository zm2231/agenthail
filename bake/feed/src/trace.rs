use std::sync::{LazyLock, Mutex, MutexGuard};

use serde_json::Value;
use time::OffsetDateTime;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Event {
    Scrim,
    Crossfade,
    Composer,
    Panels,
    Scroll,
}

const EVENTS: [Event; 5] = [Event::Scrim, Event::Crossfade, Event::Composer, Event::Panels, Event::Scroll];

impl Event {
    pub fn name(self) -> &'static str {
        match self {
            Event::Scrim => "scrim",
            Event::Crossfade => "crossfade",
            Event::Composer => "composer",
            Event::Panels => "panels",
            Event::Scroll => "scroll",
        }
    }

    pub fn fields(self) -> &'static [&'static str] {
        match self {
            Event::Scrim => &["hidden", "mask", "rows"],
            Event::Crossfade => &[],
            Event::Composer => &["height"],
            Event::Panels => &["covers", "rows", "gap", "gapAt", "top", "bottom"],
            Event::Scroll => &["y", "document", "visible"],
        }
    }

    pub fn capacity(self) -> usize {
        match self {
            Event::Scrim | Event::Crossfade | Event::Composer => 1024,
            Event::Panels | Event::Scroll => 4096,
        }
    }

    fn index(self) -> usize {
        EVENTS.iter().position(|e| *e == self).unwrap_or_default()
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Entry {
    pub at: f64,
    pub event: Event,
    pub values: [f64; 6],
}

struct Ring {
    slots: Vec<Entry>,
    capacity: usize,
    next: usize,
}

impl Ring {
    fn append(&mut self, entry: Entry) {
        if self.slots.len() < self.capacity {
            self.slots.push(entry);
        } else {
            self.slots[self.next] = entry;
        }
        self.next = (self.next + 1) % self.capacity.max(1);
    }

    fn ordered(&self) -> impl Iterator<Item = &Entry> {
        let split = if self.slots.len() < self.capacity { 0 } else { self.next };
        self.slots[split..].iter().chain(&self.slots[..split])
    }
}

pub struct Trace {
    rings: Vec<Ring>,
    last_composer: Option<f64>,
}

impl Default for Trace {
    fn default() -> Self {
        Trace::with_capacity(Event::capacity)
    }
}

pub fn shared() -> MutexGuard<'static, Trace> {
    static SHARED: LazyLock<Mutex<Trace>> = LazyLock::new(Mutex::default);
    SHARED.lock().unwrap_or_else(|e| e.into_inner())
}

impl Trace {
    pub fn with_capacity(capacity: impl Fn(Event) -> usize) -> Trace {
        Trace { rings: EVENTS.iter().map(|e| Ring { slots: Vec::new(), capacity: capacity(*e).max(1), next: 0 }).collect(), last_composer: None }
    }

    pub fn entries(&self) -> Vec<Entry> {
        let mut all: Vec<Entry> = self.rings.iter().flat_map(Ring::ordered).copied().collect();
        all.sort_by(|a, b| a.at.total_cmp(&b.at));
        all
    }

    pub fn record(&mut self, event: Event, values: &[f64], at: f64) {
        let mut padded = [0.0; 6];
        for (slot, v) in padded.iter_mut().zip(values) {
            *slot = *v;
        }
        self.rings[event.index()].append(Entry { at, event, values: padded });
    }

    pub fn composer(&mut self, height: f64, at: f64) {
        if self.last_composer == Some(height) {
            return;
        }
        self.last_composer = Some(height);
        self.record(Event::Composer, &[height], at);
    }

    pub fn lines(&self, wall: OffsetDateTime, now: f64) -> Vec<String> {
        self.entries().iter().map(|entry| line(entry, wall, now)).collect()
    }

    pub fn dump(&self, wall: OffsetDateTime, now: f64) -> String {
        self.lines(wall, now).iter().fold(String::new(), |out, l| out + l + "\n")
    }
}

fn number(v: f64) -> Value {
    if v.fract() == 0.0 && v.abs() < 9.0e15 { Value::from(v as i64) } else { Value::from(v) }
}

fn line(entry: &Entry, wall: OffsetDateTime, now: f64) -> String {
    let at = wall + time::Duration::seconds_f64(entry.at - now);
    let at = at.to_offset(time::UtcOffset::UTC);
    let stamp = format!("{:04}-{:02}-{:02}T{:02}:{:02}:{:02}.{:03}Z", at.year(), u8::from(at.month()), at.day(), at.hour(), at.minute(), at.second(), at.millisecond());
    let mut pairs: Vec<(&str, Value)> = vec![("event", Value::from(entry.event.name())), ("uptime", number(entry.at)), ("time", Value::from(stamp))];
    pairs.extend(entry.event.fields().iter().zip(entry.values).map(|(k, v)| (*k, number(v))));
    pairs.sort_by(|a, b| a.0.cmp(b.0));
    let body: Vec<String> = pairs.iter().map(|(k, v)| format!("{}:{v}", Value::from(*k))).collect();
    format!("{{{}}}", body.join(","))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_full_ring_keeps_the_newest_entries_and_never_evicts_another_events() {
        let mut trace = Trace::with_capacity(|e| if e == Event::Scroll { 4 } else { 8 });
        trace.record(Event::Crossfade, &[], 1.0);
        for y in 0..10 {
            trace.record(Event::Scroll, &[f64::from(y), 900.0, 600.0], 10.0 + f64::from(y));
        }
        let scrolls: Vec<f64> = trace.entries().iter().filter(|e| e.event == Event::Scroll).map(|e| e.values[0]).collect();
        assert_eq!(scrolls, vec![6.0, 7.0, 8.0, 9.0]);
        assert_eq!(trace.entries().iter().filter(|e| e.event == Event::Crossfade).count(), 1);
        let times: Vec<f64> = trace.entries().iter().map(|e| e.at).collect();
        assert!(times.windows(2).all(|w| w[0] <= w[1]), "entries from every ring come back in time order");
    }

    #[test]
    fn the_composer_height_is_recorded_when_it_changes() {
        let mut trace = Trace::default();
        trace.composer(44.0, 1.0);
        trace.composer(44.0, 2.0);
        trace.composer(72.0, 3.0);
        let heights: Vec<f64> = trace.entries().iter().map(|e| e.values[0]).collect();
        assert_eq!(heights, vec![44.0, 72.0]);
    }

    #[test]
    fn a_line_is_the_mac_apps_json() {
        let mut trace = Trace::with_capacity(|_| 2);
        trace.record(Event::Scroll, &[12.0, 900.0, 600.5], 99.0);
        let wall = OffsetDateTime::from_unix_timestamp(1_800_000_000).unwrap();
        let lines = trace.lines(wall, 100.0);
        assert_eq!(lines, vec![r#"{"document":900,"event":"scroll","time":"2027-01-15T07:59:59.000Z","uptime":99,"visible":600.5,"y":12}"#.to_string()]);
        let parsed: Value = serde_json::from_str(&lines[0]).unwrap();
        assert_eq!(parsed["y"], 12);
        trace.composer(44.25, 99.5);
        assert_eq!(trace.dump(wall, 100.0).lines().count(), 2);
        assert!(trace.dump(wall, 100.0).ends_with('\n'));
        assert!(trace.lines(wall, 100.0)[1].contains(r#""time":"2027-01-15T07:59:59.500Z""#));
    }
}
