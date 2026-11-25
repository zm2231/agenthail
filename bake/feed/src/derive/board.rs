use super::world::{Work, World};

pub const LANES: [&str; 4] = ["Waiting on you", "Now", "Up next", "Shipped"];
pub const LANE_CAP: usize = 5;
const SHIPPED_HOURS: f64 = 24.0;

pub struct Board<'a> {
    pub lanes: [Vec<&'a Work>; 4],
    pub stale_review: Vec<&'a Work>,
}

pub struct Fold<'a, T> {
    pub shown: &'a [T],
    pub hidden: usize,
    pub foldable: bool,
}

pub fn fold<T>(items: &[T], open: bool) -> Fold<'_, T> {
    let foldable = items.len() > LANE_CAP;
    let shown = if open { items } else { &items[..items.len().min(LANE_CAP)] };
    Fold { shown, hidden: items.len() - shown.len(), foldable }
}

fn priority(w: &Work) -> u8 {
    w.priority.trim_start_matches('P').parse().unwrap_or(9)
}

impl World {
    pub fn board(&self, project: &str) -> Board<'_> {
        let mut lanes: [Vec<&Work>; 4] = Default::default();
        let mut stale_review = Vec::new();
        for w in self.work.values().filter(|w| w.project == project) {
            match w.state.as_str() {
                "in_review" if self.is_waiting_on_you(w) => lanes[0].push(w),
                "in_review" => stale_review.push(w),
                "in_progress" => lanes[1].push(w),
                "open" | "ready" => lanes[2].push(w),
                "done" if self.hours(w.last_at()) < SHIPPED_HOURS => lanes[3].push(w),
                _ => {}
            }
        }
        for lane in lanes.iter_mut().chain([&mut stale_review]) {
            lane.sort_by(|a, b| b.last_at().cmp(a.last_at()).then(a.id.cmp(&b.id)));
        }
        lanes[2].sort_by_key(|w| priority(w));
        Board { lanes, stale_review }
    }
}
