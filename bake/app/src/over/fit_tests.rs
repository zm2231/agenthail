use super::*;

#[test]
fn a_kept_fit_matches_fitting_again_for_each_width_size_and_line_count() {
    let title = "Each poll rebuilds the overview while the highlighted entry stays where it was in the list";
    for (style, width, lines) in [(crate::theme::BODY, 240.0, 2), (crate::theme::BODY, 120.0, 2), (crate::theme::TITLE, 240.0, 2), (crate::theme::BODY, 240.0, 1)] {
        let first = fit_lines(title, style, width, lines);
        assert_eq!(first, fit_lines_now(title, style, width, lines));
        assert_eq!(fit_lines(title, style, width, lines), first, "a second fit gives the kept one");
    }
    assert_ne!(fit_lines(title, crate::theme::BODY, 240.0, 2), fit_lines(title, crate::theme::BODY, 120.0, 2), "a narrower width fits apart");
    assert_ne!(fit_lines(title, crate::theme::BODY, 240.0, 2), fit_lines(title, crate::theme::BODY, 240.0, 1), "fewer lines fit apart");
}
