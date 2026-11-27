use super::*;

#[test]
fn settings_ages_move_on_each_minute_and_wake_for_the_next() {
    let mut s = init();
    s.minute = time::OffsetDateTime::now_utc() - time::Duration::minutes(2);
    crate::wake::begin(crate::wake::Window::Settings);
    assert!(minute(&mut s), "a minute that turned redraws");
    assert!(!minute(&mut s), "the same minute doesn't");
    let next = crate::wake::next(crate::wake::Window::Settings).expect("the next minute is woken for");
    assert!(next.saturating_duration_since(std::time::Instant::now()) <= std::time::Duration::from_secs(60));
}
