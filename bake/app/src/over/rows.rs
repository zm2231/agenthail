use std::hash::Hash;

use rune::El;

use crate::over::Target;

pub fn memo(id: impl Into<String>, shows: impl Hash, build: impl FnOnce() -> El) -> El {
    let id = id.into();
    #[cfg(test)]
    let named = id.clone();
    #[cfg(all(test, feature = "frame-profile"))]
    built::called(&id);
    let v = fx(&(shows, crate::theme::paint_key()));
    rune::memo::memo_with_hash(id, v, move || {
        #[cfg(all(test, feature = "frame-profile"))]
        built::rebuilt(&named);
        #[cfg(test)]
        built::note(named);
        build()
    })
}

pub struct Part<'a> {
    id: String,
    v: Option<u64>,
    row: bool,
    build: Box<dyn FnOnce() -> El + 'a>,
}

impl<'a> Part<'a> {
    pub fn row(id: impl Into<String>, shows: impl Hash, build: impl FnOnce() -> El + 'a) -> Part<'a> {
        Part { id: id.into(), v: Some(fx(&shows)), row: true, build: Box::new(build) }
    }

    pub fn group(id: impl Into<String>, v: Option<u64>, build: impl FnOnce() -> El + 'a) -> Part<'a> {
        Part { id: id.into(), v, row: false, build: Box::new(build) }
    }

    pub fn always(id: impl Into<String>, build: impl FnOnce() -> El + 'a) -> Part<'a> {
        Part { id: id.into(), v: None, row: false, build: Box::new(build) }
    }

    pub fn v(&self) -> Option<u64> {
        self.v.map(|v| fx(&(&self.id, v)))
    }

    pub fn el(self) -> El {
        let Part { id, v, row, build } = self;
        let Some(v) = v else { return build() };
        #[cfg(all(test, feature = "frame-profile"))]
        built::called(&id);
        #[cfg(all(test, feature = "frame-profile"))]
        let any = id.clone();
        #[cfg(test)]
        let named = row.then(|| id.clone());
        #[cfg(not(test))]
        let _ = row;
        rune::memo::memo_with_hash(id, fx(&(v, crate::theme::paint_key())), move || {
            #[cfg(all(test, feature = "frame-profile"))]
            built::rebuilt(&any);
            #[cfg(test)]
            if let Some(named) = named {
                built::note(named);
            }
            build()
        })
    }
}

pub fn hash(v: &impl Hash) -> u64 {
    fx(v)
}

pub fn version<'p, 'q: 'p>(shows: impl Hash, parts: impl IntoIterator<Item = &'p Part<'q>>) -> Option<u64> {
    parts.into_iter().try_fold(fx(&shows), |h, p| p.v().map(|v| fx(&(h, v))))
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
        static VIEWS: std::cell::Cell<u64> = const { std::cell::Cell::new(0) };
        #[cfg(feature = "frame-profile")]
        static CALLED: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
        #[cfg(feature = "frame-profile")]
        static REBUILT: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
    }

    #[cfg(feature = "frame-profile")]
    pub fn rebuilt(id: &str) {
        REBUILT.with_borrow_mut(|c| c.push(id.to_string()));
    }

    #[cfg(feature = "frame-profile")]
    pub fn take_rebuilt() -> Vec<String> {
        REBUILT.with_borrow_mut(std::mem::take)
    }

    #[cfg(feature = "frame-profile")]
    pub fn called(id: &str) {
        CALLED.with_borrow_mut(|c| c.push(id.to_string()));
    }

    #[cfg(feature = "frame-profile")]
    pub fn take_called() -> Vec<String> {
        CALLED.with_borrow_mut(std::mem::take)
    }


    pub fn viewed() {
        VIEWS.set(VIEWS.get() + 1);
    }

    pub fn views() -> u64 {
        VIEWS.get()
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
        self.write_usize(bytes.len());
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
