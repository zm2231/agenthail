use std::collections::{BTreeSet, HashMap};

use anyhow::Result;
use serde_json::{Map, Value, json};

use crate::catalog::{Client, parse_time};
use crate::derive::world::Recorded;

const LEDGER_WINDOW_H: f64 = 72.0;
const DONE_KEPT: usize = 40;
const RECENT_DAYS: i64 = 4;

pub fn recorded(client: &Client) -> Result<Recorded> {
    let snap = client.get_json("/api/v1/snapshot")?;
    let now = snap["updatedAt"].as_str().and_then(parse_time).unwrap_or_else(time::OffsetDateTime::now_utc);
    let sessions = snap["sessions"].as_array().cloned().unwrap_or_default();
    let age_h = |s: &Value| s["lastActive"].as_str().and_then(parse_time).map(|t| (now - t).as_seconds_f64() / 3600.0).unwrap_or(1e9);
    let live: BTreeSet<String> = sessions.iter().filter(|s| age_h(s) < LEDGER_WINDOW_H).filter_map(|s| s["project"]["id"].as_str().map(String::from)).collect();
    let keys: Vec<String> = snap["projects"].as_array().into_iter().flatten().filter(|p| p["archived"] != json!(true)).filter_map(|p| p["id"].as_str().map(String::from)).filter(|k| live.contains(k)).collect();
    let cutoff = now - time::Duration::days(RECENT_DAYS);
    let ledgers: Map<String, Value> = std::thread::scope(|scope| {
        let jobs: Vec<_> = keys.iter().map(|k| (k.clone(), scope.spawn(|| client.ledger_rows(k)))).collect();
        jobs.into_iter().filter_map(|(k, h)| h.join().ok().and_then(Result::ok).map(|rows| (k, Value::Array(trim(rows, cutoff))))).collect()
    });
    let usage: Vec<Value> = client
        .get_json("/api/v1/usage")
        .ok()
        .and_then(|u| u["sets"].as_array().cloned())
        .into_iter()
        .flatten()
        .flat_map(|st| {
            st["meters"].as_array().cloned().unwrap_or_default().into_iter().map(move |m| json!({"provider": m["provider"], "label": m["label"], "percent": m["percent"], "resetsAt": m["resetsAt"], "name": st["name"]}))
        })
        .collect();
    let hosts: Vec<Value> = client
        .get_json("/api/v1/hosts")
        .ok()
        .map(|hv| std::iter::once(json!({"name": hv["host"]["name"], "state": hv["host"]["state"]})).chain(hv["hosts"].as_array().cloned().unwrap_or_default().into_iter().map(|x| json!({"name": x["name"], "state": x["state"]}))).collect())
        .unwrap_or_default();
    let actors = actors(client, &sessions, &ledgers);
    let raw = json!({"updatedAt": snap["updatedAt"], "projects": snap["projects"], "sessions": sessions, "ledgers": ledgers, "usage": usage, "hosts": hosts, "actors": actors});
    Ok(serde_json::from_value(raw)?)
}

fn trim(rows: Vec<Value>, cutoff: time::OffsetDateTime) -> Vec<Value> {
    let last_at = |r: &Value| r["timeline"].as_array().and_then(|t| t.last()).and_then(|e| e["at"].as_str()).map(String::from).unwrap_or_default();
    let recent = |r: &Value| parse_time(&last_at(r)).is_some_and(|t| t >= cutoff);
    let mut done: Vec<&Value> = rows.iter().filter(|r| r["state"] == "done").collect();
    done.sort_by_key(|r| last_at(r));
    let kept: BTreeSet<String> = done.iter().rev().take(DONE_KEPT).filter_map(|r| r["id"].as_str().map(String::from)).collect();
    rows.into_iter().filter(|r| !matches!(r["state"].as_str(), Some("done" | "deleted")) || r["id"].as_str().is_some_and(|id| kept.contains(id)) || recent(r)).collect()
}

fn actors(client: &Client, sessions: &[Value], ledgers: &Map<String, Value>) -> HashMap<String, Value> {
    let known: BTreeSet<&str> = sessions.iter().filter_map(|s| s["id"].as_str()).collect();
    let wanted: BTreeSet<String> = ledgers
        .values()
        .filter_map(Value::as_array)
        .flatten()
        .flat_map(|it| it["timeline"].as_array().cloned().unwrap_or_default())
        .filter_map(|t| t["actor"].as_str().map(String::from))
        .filter(|a| !a.is_empty() && !known.contains(a.as_str()) && !a.contains(' ') && !a.starts_with("td:"))
        .collect();
    std::thread::scope(|scope| {
        let jobs: Vec<_> = wanted.iter().map(|a| (a.clone(), scope.spawn(|| client.get_json(&format!("/api/v1/session?id={}", crate::catalog::enc(a)))))).collect();
        jobs.into_iter()
            .filter_map(|(a, h)| {
                let v = h.join().ok()?.ok()?;
                let x = &v["session"];
                Some((a, json!({"alias": v["alias"], "name": x["name"], "surface": x["surface"]})))
            })
            .collect()
    })
}
