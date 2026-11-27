use rune::Tx;

#[derive(Default)]
pub struct Wake(Option<f64>);

impl Wake {
    pub fn at(&mut self, tx: &mut Tx, due: f64) {
        if self.0.is_none_or(|armed| tx.clock() >= armed || armed > due + 0.001) {
            self.0 = Some(due);
            tx.wake_at(due);
        }
    }

    pub fn after(&mut self, tx: &mut Tx, step: std::time::Duration) {
        let due = tx.clock() + step.as_secs_f64();
        self.at(tx, due);
    }
}
