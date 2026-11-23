use std::io::Write;
use std::time::Instant;

pub struct Metrics {
    started: Instant,
    latencies: Vec<f64>,
    batches: usize,
    renders: usize,
    log: Option<std::fs::File>,
}

impl Metrics {
    pub fn new() -> Self {
        let log = std::env::var("BAKE_LOG").ok().and_then(|p| std::fs::File::create(p).ok());
        Self { started: Instant::now(), latencies: Vec::new(), batches: 0, renders: 0, log }
    }

    pub fn batch(&mut self) {
        self.batches += 1;
    }

    pub fn render(&mut self) {
        self.renders += 1;
    }

    pub fn painted(&mut self, received: Instant, blocks: usize) {
        let ms = received.elapsed().as_secs_f64() * 1000.0;
        self.latencies.push(ms);
        if let Some(log) = &mut self.log {
            let _ = writeln!(log, "{:.3}\tpaint\t{ms:.2}\t{blocks}", self.started.elapsed().as_secs_f64());
        }
    }

    pub fn summary(&mut self, label: &str) -> String {
        let mut sorted = self.latencies.clone();
        sorted.sort_by(|a, b| a.partial_cmp(b).unwrap());
        let pick = |q: f64| sorted.get(((sorted.len() as f64 - 1.0) * q).round() as usize).copied().unwrap_or(0.0);
        let line = format!(
            "{label}: batches={} renders={} paint p50={:.1}ms p95={:.1}ms max={:.1}ms after {:.1}s",
            self.batches,
            self.renders,
            pick(0.5),
            pick(0.95),
            sorted.last().copied().unwrap_or(0.0),
            self.started.elapsed().as_secs_f64()
        );
        if let Some(log) = &mut self.log {
            let _ = writeln!(log, "{line}");
        }
        line
    }
}
