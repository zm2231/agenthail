use feed::blocks::{Kind, Options, build};
use feed::item::Item;

fn items(raw: serde_json::Value) -> Vec<Item> {
    serde_json::from_value(raw).unwrap()
}

fn shape(items: &[Item]) -> Vec<(String, String)> {
    build(items, &Options { focus: false, ..Options::default() })
        .into_iter()
        .map(|b| {
            let kind = match &b.kind {
                Kind::User(t) => format!("user {t}"),
                Kind::Assistant(t) => format!("assistant {t}"),
                Kind::Files(files) => format!("files {}", files.iter().map(|f| format!("{}={:?}", f.name, f.bytes)).collect::<Vec<_>>().join(",")),
                Kind::Image(a) => format!("image {} {} {}x{}", a.id, a.media_type, a.width, a.height),
                Kind::File(item) => format!("file {}", feed::attach::sent_name(item)),
                Kind::Annotation(t) => format!("note {t}"),
                other => format!("{other:?}"),
            };
            (b.id, kind)
        })
        .collect()
}

#[test]
fn sent_images_and_files_become_their_own_rows() {
    let got = shape(&items(serde_json::json!([
        {"id": "u1", "kind": "message", "role": "user", "text": "look at these", "files": [{"name": "notes.md", "path": "/up/notes.md", "bytes": 12}]},
        {"id": "a1", "kind": "attachment", "role": "user", "title": "Image", "text": "Image attachment", "attachment": {"id": "att-1", "mediaType": "image/png", "width": 640, "height": 480, "bytes": 2048}},
        {"id": "r1", "kind": "message", "role": "assistant", "text": "Here is the report"},
        {"id": "f1", "kind": "file", "role": "assistant", "title": "report.pdf", "text": "The weekly report", "files": [{"name": "report.pdf", "path": "/w/report.pdf", "bytes": 900}], "attachment": {"id": "att-2", "mediaType": "application/pdf", "bytes": 900, "name": "report.pdf"}},
        {"id": "a2", "kind": "attachment", "role": "assistant", "title": "Image", "text": "A chart", "attachment": {"id": "att-3", "mediaType": "image/jpeg"}},
        {"id": "a3", "kind": "attachment", "role": "user", "title": "Image", "text": "Image attachment"}
    ])));
    let want: Vec<(String, String)> = [
        ("item:u1", "user look at these"),
        ("files:u1", "files notes.md=Some(12)"),
        ("item:a1", "image att-1 image/png 640x480"),
        ("item:r1", "assistant Here is the report"),
        ("caption:f1", "assistant The weekly report"),
        ("item:f1", "file report.pdf"),
        ("caption:a2", "assistant A chart"),
        ("item:a2", "image att-3 image/jpeg 0x0"),
        ("item:a3", "note Image attached"),
    ]
    .iter()
    .map(|(a, b)| (a.to_string(), b.to_string()))
    .collect();
    assert_eq!(got, want);
}

#[test]
fn a_message_of_only_files_still_shows_them() {
    let got = shape(&items(serde_json::json!([
        {"id": "u1", "kind": "message", "role": "user", "text": "", "files": [{"name": "a.zip", "path": "/up/a.zip"}]}
    ])));
    assert_eq!(got, vec![("files:u1".to_string(), "files a.zip=None".to_string())]);
}
