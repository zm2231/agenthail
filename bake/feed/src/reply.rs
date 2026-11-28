use serde::Deserializer;
use serde::de::{DeserializeOwned, DeserializeSeed, Error as _, IntoDeserializer, MapAccess, SeqAccess, Visitor};
use serde_json::{Value, map};

// The daemon writes an empty list as null or omits it.
pub fn parse<T: DeserializeOwned>(value: Value) -> serde_json::Result<T> {
    T::deserialize(Lenient(value))
}

pub fn member<T: DeserializeOwned>(reply: &Value, key: &str) -> serde_json::Result<T> {
    match reply.get(key) {
        Some(value) => parse(value.clone()),
        None => Err(serde_json::Error::custom(format!("missing field `{key}`"))),
    }
}

pub fn parse_str<T: DeserializeOwned>(text: &str) -> serde_json::Result<T> {
    parse(serde_json::from_str(text)?)
}

struct Lenient(Value);

impl<'de> Deserializer<'de> for Lenient {
    type Error = serde_json::Error;

    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        match self.0 {
            Value::Array(list) => {
                let len = list.len();
                let mut items = Items(list.into_iter());
                let out = visitor.visit_seq(&mut items)?;
                match items.0.len() {
                    0 => Ok(out),
                    _ => Err(serde_json::Error::invalid_length(len, &"fewer elements in the list")),
                }
            }
            Value::Object(members) => visitor.visit_map(Members { members: members.into_iter(), value: None }),
            other => other.deserialize_any(visitor),
        }
    }

    fn deserialize_option<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        match self.0 {
            Value::Null => visitor.visit_none(),
            _ => visitor.visit_some(self),
        }
    }

    fn deserialize_seq<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        match self.0 {
            Value::Null => visitor.visit_seq(Items(Vec::new().into_iter())),
            _ => self.deserialize_any(visitor),
        }
    }

    fn deserialize_map<V: Visitor<'de>>(self, visitor: V) -> serde_json::Result<V::Value> {
        match self.0 {
            Value::Null => visitor.visit_map(Members { members: map::Map::new().into_iter(), value: None }),
            _ => self.deserialize_any(visitor),
        }
    }

    fn deserialize_newtype_struct<V: Visitor<'de>>(self, _name: &'static str, visitor: V) -> serde_json::Result<V::Value> {
        visitor.visit_newtype_struct(self)
    }

    fn deserialize_enum<V: Visitor<'de>>(self, name: &'static str, variants: &'static [&'static str], visitor: V) -> serde_json::Result<V::Value> {
        self.0.deserialize_enum(name, variants, visitor)
    }

    serde::forward_to_deserialize_any! {
        bool i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char str string bytes byte_buf unit unit_struct tuple tuple_struct struct identifier ignored_any
    }
}

struct Items(std::vec::IntoIter<Value>);

impl<'de> SeqAccess<'de> for Items {
    type Error = serde_json::Error;

    fn next_element_seed<S: DeserializeSeed<'de>>(&mut self, seed: S) -> serde_json::Result<Option<S::Value>> {
        self.0.next().map(|v| seed.deserialize(Lenient(v))).transpose()
    }

    fn size_hint(&self) -> Option<usize> {
        Some(self.0.len())
    }
}

struct Members {
    members: map::IntoIter,
    value: Option<Value>,
}

impl<'de> MapAccess<'de> for Members {
    type Error = serde_json::Error;

    fn next_key_seed<S: DeserializeSeed<'de>>(&mut self, seed: S) -> serde_json::Result<Option<S::Value>> {
        let Some((key, value)) = self.members.next() else { return Ok(None) };
        self.value = Some(value);
        seed.deserialize(IntoDeserializer::<serde_json::Error>::into_deserializer(key)).map(Some)
    }

    fn next_value_seed<S: DeserializeSeed<'de>>(&mut self, seed: S) -> serde_json::Result<S::Value> {
        seed.deserialize(Lenient(self.value.take().unwrap_or(Value::Null)))
    }

    fn size_hint(&self) -> Option<usize> {
        Some(self.members.len())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;
    use serde_json::json;
    use std::collections::{BTreeMap, HashMap};

    #[derive(Deserialize, Debug, Default, PartialEq)]
    struct Row {
        id: String,
        #[serde(default)]
        tags: Vec<String>,
        #[serde(default)]
        note: Option<String>,
        #[serde(default)]
        counts: HashMap<String, u64>,
        #[serde(default)]
        input: Value,
    }

    #[derive(Deserialize, Debug, PartialEq)]
    struct Kept {
        id: String,
        #[serde(flatten)]
        rest: serde_json::Map<String, Value>,
    }

    #[derive(Deserialize, Debug, PartialEq)]
    #[serde(rename_all = "lowercase")]
    enum Kind {
        Send,
        Wait(u64),
    }

    #[test]
    fn a_null_list_or_map_reads_as_empty_at_any_depth() {
        let rows: Vec<Row> = parse(json!([{"id": "a", "tags": null, "note": null, "counts": null}])).unwrap();
        assert_eq!(rows, vec![Row { id: "a".into(), ..Row::default() }]);
        let nested: BTreeMap<String, Vec<Row>> = parse(json!({"p": [{"id": "b", "tags": null}]})).unwrap();
        assert!(nested["p"][0].tags.is_empty());
        assert_eq!(parse::<Vec<Vec<String>>>(json!([null, ["x"]])).unwrap(), vec![Vec::new(), vec!["x".to_string()]]);
    }

    #[test]
    fn a_missing_value_reads_as_its_empty_form() {
        assert_eq!(parse::<Vec<Row>>(Value::Null).unwrap(), Vec::new());
        assert_eq!(parse::<HashMap<String, u64>>(Value::Null).unwrap(), HashMap::new());
        assert_eq!(parse::<Option<Row>>(Value::Null).unwrap(), None);
        assert_eq!(parse::<Option<Vec<String>>>(Value::Null).unwrap(), None);
        assert!(parse::<Row>(Value::Null).is_err(), "a missing object stays missing");
    }

    #[test]
    fn raw_data_keeps_its_nulls() {
        let row: Row = parse(json!({"id": "a", "input": {"command": "deploy", "target": null, "args": [null]}})).unwrap();
        assert_eq!(row.input, json!({"command": "deploy", "target": null, "args": [null]}));
        assert_eq!(parse::<Value>(json!({"x": null})).unwrap(), json!({"x": null}));
        let kept: Kept = parse(json!({"id": "r", "future": null, "more": {"deep": null}})).unwrap();
        assert_eq!(Value::Object(kept.rest), json!({"future": null, "more": {"deep": null}}));
        assert_eq!(member::<Vec<String>>(&parse::<Value>(json!({"rows": null})).unwrap(), "rows").unwrap(), Vec::<String>::new());
    }

    #[test]
    fn types_that_require_a_value_still_do() {
        assert!(parse::<Row>(json!({"id": null})).is_err());
        assert!(parse::<(String, u64)>(Value::Null).is_err());
        assert!(parse::<Kind>(Value::Null).is_err());
        assert_eq!(parse::<Vec<Kind>>(json!(["send", {"wait": 3}])).unwrap(), vec![Kind::Send, Kind::Wait(3)]);
        assert!(parse::<Vec<String>>(json!(["a", null])).is_err());
        assert!(serde_json::from_value::<Vec<Row>>(Value::Null).is_err());
    }

    #[test]
    fn a_null_member_is_empty_and_a_missing_one_is_unreadable() {
        assert_eq!(member::<Vec<Row>>(&json!({"rows": null}), "rows").unwrap(), Vec::new());
        assert!(member::<Vec<Row>>(&json!({}), "rows").unwrap_err().to_string().contains("missing field `rows`"));
    }
}
