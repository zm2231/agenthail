use std::hash::Hash;

use rune::El;
use rune_kit::Kit;

use crate::over::Target;

pub fn memo(id: impl Into<String>, shows: impl Hash, k: &Kit, build: impl FnOnce() -> El) -> El {
    let id = id.into();
    #[cfg(test)]
    let named = id.clone();
    let look = fx(&(crate::theme::paint_key(), k.dark, k.c, k.ty, k.radius, k.roles, k.text_scale.to_bits()));
    let v = fx(&(shows, look));
    rune::memo::memo_with_hash(id, v, move || {
        #[cfg(test)]
        built::note(named);
        build()
    })
}

pub fn target_id(t: &Target) -> String {
    match t {
        Target::Session(id) => format!("s:{id}"),
        Target::Work(w) => format!("w:{}|{}", w.ledger, w.id),
        Target::Project(key) => format!("p:{key}"),
    }
}

#[cfg(test)]
pub mod built {
    use std::cell::RefCell;

    thread_local! {
        static BUILT: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
    }

    pub fn note(id: String) {
        BUILT.with_borrow_mut(|b| b.push(id));
    }

    pub fn take() -> Vec<String> {
        BUILT.with_borrow_mut(std::mem::take)
    }
}

fn fx(v: &impl Hash) -> u64 {
    let mut h = Fx(0);
    v.hash(&mut h);
    std::hash::Hasher::finish(&h)
}

struct Fx(u64);

impl std::hash::Hasher for Fx {
    fn write(&mut self, bytes: &[u8]) {
        for chunk in bytes.chunks(8) {
            let mut word = [0u8; 8];
            word[..chunk.len()].copy_from_slice(chunk);
            self.write_u64(u64::from_le_bytes(word));
        }
    }

    fn write_u8(&mut self, n: u8) {
        self.write_u64(n.into());
    }

    fn write_u32(&mut self, n: u32) {
        self.write_u64(n.into());
    }

    fn write_u64(&mut self, n: u64) {
        self.0 = (self.0.rotate_left(5) ^ n).wrapping_mul(0x51_7c_c1_b7_27_22_0a_95);
    }

    fn write_usize(&mut self, n: usize) {
        self.write_u64(n as u64);
    }

    fn finish(&self) -> u64 {
        self.0
    }
}
