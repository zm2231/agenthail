use std::io::{BufRead, BufReader, Read, Write};
use std::net::TcpListener;
use std::sync::mpsc;

use serde_json::Value;

use crate::catalog::Client;

pub fn daemon(replies: Vec<(u16, String)>) -> (Client, mpsc::Receiver<(String, Value)>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let (tx, rx) = mpsc::channel();
    std::thread::spawn(move || {
        for (status, body) in replies {
            let (stream, _) = listener.accept().unwrap();
            let mut reader = BufReader::new(stream.try_clone().unwrap());
            let mut line = String::new();
            reader.read_line(&mut line).unwrap();
            let path = line.split_whitespace().nth(1).unwrap_or_default().to_string();
            let mut length = 0usize;
            loop {
                let mut header = String::new();
                reader.read_line(&mut header).unwrap();
                let header = header.trim_end();
                if header.is_empty() {
                    break;
                }
                match header.split_once(':') {
                    Some((name, value)) if name.eq_ignore_ascii_case("content-length") => length = value.trim().parse().unwrap(),
                    _ => {}
                }
            }
            let mut raw = vec![0; length];
            reader.read_exact(&mut raw).unwrap();
            tx.send((path, serde_json::from_slice(&raw).unwrap_or(Value::Null))).unwrap();
            let mut stream = stream;
            write!(stream, "HTTP/1.1 {status} X\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
        }
    });
    (Client::new(&base, "secret"), rx)
}

pub fn replies(list: &[(u16, &str)]) -> Vec<(u16, String)> {
    list.iter().map(|(s, b)| (*s, b.to_string())).collect()
}
