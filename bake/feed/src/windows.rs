use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Frame {
    pub x: i32,
    pub y: i32,
    pub width: u32,
    pub height: u32,
    #[serde(default = "one")]
    pub scale: f64,
    #[serde(default)]
    pub maximized: bool,
    #[serde(default)]
    pub full_screen: bool,
}

fn one() -> f64 {
    1.0
}

impl Frame {
    pub fn size_points(&self) -> (f32, f32) {
        let scale = if self.scale > 0.0 { self.scale } else { 1.0 };
        ((self.width as f64 / scale) as f32, (self.height as f64 / scale) as f32)
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Saved {
    pub session: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub frame: Option<Frame>,
}

pub fn read(v: &serde_json::Value) -> Vec<Saved> {
    let Some(list) = v.as_array() else { return Vec::new() };
    let mut out: Vec<Saved> = Vec::new();
    for w in list.iter().filter_map(|w| serde_json::from_value::<Saved>(w.clone()).ok()) {
        if !w.session.is_empty() && !out.iter().any(|o| o.session == w.session) {
            out.push(w);
        }
    }
    out
}

pub fn write(list: &[Saved]) -> serde_json::Value {
    serde_json::to_value(list).unwrap_or(serde_json::Value::Null)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn saved_windows_round_trip_and_skip_bad_or_repeated_rows() {
        let list = vec![
            Saved { session: "a".into(), frame: Some(Frame { x: 10, y: -20, width: 1800, height: 1640, scale: 2.0, maximized: false, full_screen: true }) },
            Saved { session: "b".into(), frame: None },
        ];
        assert_eq!(read(&write(&list)), list);
        let messy = json!([{"session": "a"}, {"session": ""}, {"nope": 1}, {"session": "a", "frame": {"x": 1, "y": 2, "width": 3, "height": 4}}, 7]);
        assert_eq!(read(&messy), vec![Saved { session: "a".into(), frame: None }]);
        assert!(read(&json!(null)).is_empty());
    }

    #[test]
    fn a_frame_without_a_scale_reads_as_points() {
        let f: Frame = serde_json::from_value(json!({"x": 0, "y": 0, "width": 900, "height": 820})).unwrap();
        assert_eq!((f.scale, f.size_points()), (1.0, (900.0, 820.0)));
        let retina = Frame { width: 1800, height: 1640, scale: 2.0, ..f };
        assert_eq!(retina.size_points(), (900.0, 820.0));
    }
}
