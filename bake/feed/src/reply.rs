use serde::Deserializer;
use serde::de::{DeserializeOwned, Visitor};
use serde_json::{Map, Value};

// The daemon writes an empty list as null or omits it.
pub fn parse<T: DeserializeOwned>(mut value: Value) -> serde_json::Result<T> {
    drop_nulls(&mut value);
    if !value.is_null() {
        return serde_json::from_value(value);
    }
    T::deserialize(Absent)
}

struct Absent;

impl<'de> Deserializer<'de> for Absent {
    type Error = serde_json::Error;

    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        Value::Null.deserialize_any(visitor)
    }

    fn deserialize_option<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        visitor.visit_none()
    }

    fn deserialize_seq<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        Value::Array(Vec::new()).deserialize_seq(visitor)
    }

    fn deserialize_map<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        Value::Object(Map::new()).deserialize_map(visitor)
    }

    serde::forward_to_deserialize_any! {
        bool i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char str string bytes byte_buf unit unit_struct newtype_struct tuple tuple_struct struct enum identifier ignored_any
    }
}

pub fn member<T: DeserializeOwned>(reply: &Value, key: &str) -> serde_json::Result<T> {
    match reply.get(key) {
        Some(value) => parse(value.clone()),
        None => Err(serde::de::Error::custom(format!("missing field `{key}`"))),
    }
}

pub fn parse_str<T: DeserializeOwned>(text: &str) -> serde_json::Result<T> {
    parse(serde_json::from_str(text)?)
}

fn drop_nulls(value: &mut Value) {
    match value {
        Value::Object(map) => {
            map.retain(|_, v| !v.is_null());
            map.values_mut().for_each(drop_nulls);
        }
        Value::Array(list) => list.iter_mut().for_each(drop_nulls),
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;
    use serde_json::json;
    use std::collections::HashMap;

    #[derive(Deserialize, Debug, Default, PartialEq)]
    struct Row {
        id: String,
        #[serde(default)]
        tags: Vec<String>,
        #[serde(default)]
        note: Option<String>,
    }

    #[test]
    fn a_null_member_reads_as_absent_at_any_depth() {
        let rows: Vec<Row> = parse(json!([{"id": "a", "tags": null, "note": null}])).unwrap();
        assert_eq!(rows, vec![Row { id: "a".into(), ..Row::default() }]);
        let nested: HashMap<String, Vec<Row>> = parse(json!({"p": [{"id": "b", "tags": null}]})).unwrap();
        assert_eq!(nested["p"][0].tags, Vec::<String>::new());
    }

    #[test]
    fn a_missing_value_reads_as_its_empty_form() {
        assert_eq!(parse::<Vec<Row>>(Value::Null).unwrap(), Vec::new());
        assert_eq!(parse::<HashMap<String, u64>>(Value::Null).unwrap(), HashMap::new());
        assert_eq!(parse::<Option<Row>>(Value::Null).unwrap(), None);
        assert!(parse::<Row>(Value::Null).is_err(), "a missing object stays missing");
        assert_eq!(parse::<Option<Vec<String>>>(Value::Null).unwrap(), None);
        assert_eq!(parse::<Value>(Value::Null).unwrap(), Value::Null);
    }

    #[test]
    fn a_member_the_type_requires_stays_required() {
        assert!(parse::<Row>(json!({"id": null})).is_err());
        assert!(serde_json::from_value::<Vec<Row>>(Value::Null).is_err());
    }

    #[test]
    fn a_null_member_is_empty_and_a_missing_one_is_unreadable() {
        assert_eq!(member::<Vec<Row>>(&json!({"rows": null}), "rows").unwrap(), Vec::new());
        assert!(member::<Vec<Row>>(&json!({}), "rows").unwrap_err().to_string().contains("missing field `rows`"));
    }
}
