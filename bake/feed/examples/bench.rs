use std::time::Instant;

use feed::wire::{Outcome, Rebuild, Sse, Wire};

fn main() {
    let path = std::env::args().nth(1).expect("stream.tsv");
    let text = std::fs::read_to_string(path).expect("read");
    let mut sse = Sse::default();
    let mut rebuild = Rebuild::default();
    let mut transcript = feed::Transcript::default();
    let mut applies = Vec::new();
    let mut builds = Vec::new();
    let mut first = true;
    for line in text.lines() {
        let Some((_, rest)) = line.split_once('\t') else { continue };
        let Some(json) = sse.line(rest) else { continue };
        let Ok(wire) = serde_json::from_str::<Wire>(&json) else { continue };
        if let Outcome::Events(seq, events) = rebuild.apply(wire) {
            let a = Instant::now();
            transcript.apply(seq, events);
            applies.push(a.elapsed().as_secs_f64() * 1000.0);
            if transcript.items().len() > 3000 {
                if first {
                    first = false;
                    eprintln!("history loaded: {} items", transcript.items().len());
                }
                let b = Instant::now();
                let blocks = transcript.blocks(true);
                builds.push(b.elapsed().as_secs_f64() * 1000.0);
                std::hint::black_box(blocks);
            }
        }
    }
    builds.sort_by(|a, b| a.partial_cmp(b).unwrap());
    let pick = |q: f64| builds[((builds.len() as f64 - 1.0) * q) as usize];
    println!("blocks() over {} items: p50 {:.2} ms p95 {:.2} ms ({} rebuilds)", transcript.items().len(), pick(0.5), pick(0.95), builds.len());
}
