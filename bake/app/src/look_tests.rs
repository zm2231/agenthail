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
