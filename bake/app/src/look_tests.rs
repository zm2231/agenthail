use super::*;

#[test]
fn the_same_appearance_problem_posted_again_does_not_redraw() {
    let (tx, rx) = crate::wake::channel();
    let mut s = State { rx: Some(rx), ..State::default() };
    let problem = Some("Couldn't read appearance settings: down".to_string());
    tx.send(Got::Problem(problem.clone())).unwrap();
    pump(&mut s);
    let shown = s.generation;
    tx.send(Got::Problem(problem)).unwrap();
    pump(&mut s);
    assert_eq!(s.generation, shown, "a retry that fails the same way changes nothing");
    tx.send(Got::Problem(None)).unwrap();
    pump(&mut s);
    assert_eq!(s.generation, shown + 1, "the problem clearing redraws");
}

#[test]
fn kits_follow_the_system_increased_contrast_without_lifting_the_looks_colours_again() {
    for paint in [Paint::dark(), Paint::light()] {
        let roles = Kit::neutral(paint.dark).roles;
        for translucent in [false, true] {
            let normal = KitEnv::new(paint.dark);
            let increased = normal.increased_contrast(true);
            let plain = main_kit(&normal, &paint, roles, translucent);
            let main = main_kit(&increased, &paint, roles, translucent);
            assert!(!plain.increased_contrast);
            assert!(main.increased_contrast, "code and diffs in the main kit hold 7:1");
            assert_ne!(main.c.icon_muted, plain.c.icon_muted, "the kit's own muted icons lift");
            assert_eq!((main.c.text_base, main.c.text_muted, main.c.text_faint), (paint.text, paint.muted, paint.faint), "the look's colours, already picked for increased contrast, are not lifted again");
            assert_eq!(main.roles, roles);
            let plain = chrome_kit(&normal, &paint, roles, translucent, true);
            let chrome = chrome_kit(&increased, &paint, roles, translucent, true);
            assert!(!plain.increased_contrast);
            assert!(chrome.increased_contrast, "code and diffs in the chrome kit hold 7:1");
            assert_eq!((chrome.c.text_base, chrome.c.text_weak), (paint.text, paint.muted));
            assert_eq!(chrome.roles, roles);
        }
    }
}
