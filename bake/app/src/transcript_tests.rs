use rune_kit::Kit;
use rune_kit::syntax::{Syntax, SyntaxTheme};

use super::markdown_kit;

#[test]
fn code_blocks_follow_the_window_kits_increased_contrast() {
    for dark in [true, false] {
        let window = Kit::neutral(dark);
        let normal = markdown_kit(dark, &window);
        let increased = markdown_kit(dark, &window.increased_contrast());
        assert!(!normal.increased_contrast);
        assert!(increased.increased_contrast, "code blocks hold 7:1 when the system asks for more contrast");
        assert_ne!(SyntaxTheme::from_kit(&increased).color(Syntax::Comment), SyntaxTheme::from_kit(&normal).color(Syntax::Comment), "comments read more strongly");
        assert_eq!(increased.roles, window.increased_contrast().roles);
    }
}
