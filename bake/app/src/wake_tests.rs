use std::time::{Duration, Instant};

use super::*;

#[test]
fn a_deadline_that_passes_before_the_poll_ends_still_wakes_it_at_once() {
    begin(Window::Main);
    let at = Instant::now() + Duration::from_millis(5);
    assert!(!due(at));
    std::thread::sleep(Duration::from_millis(10));
    assert_eq!(next(Window::Main), Some(at), "a deadline that came due while the poll ran is kept, so the wake is immediate");
}

#[test]
fn each_poll_starts_with_only_its_own_deadlines() {
    begin(Window::Main);
    assert!(!due(Instant::now() + Duration::from_secs(60)));
    begin(Window::Main);
    assert_eq!(next(Window::Main), None, "a deadline no check asked for again arms nothing");
}

#[test]
fn settings_deadlines_do_not_wake_main() {
    begin(Window::Settings);
    let at = Instant::now() + Duration::from_secs(5);
    assert!(!due(at));
    assert_eq!(next(Window::Settings), Some(at));
    begin(Window::Main);
    assert_eq!(next(Window::Main), None);
}

#[test]
fn a_clock_deadline_too_far_to_measure_arms_nothing() {
    begin(Window::Main);
    assert!(!due_clock(f64::MAX, 0.0));
    assert_eq!(next(Window::Main), None);
}
