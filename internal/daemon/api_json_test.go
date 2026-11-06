package daemon

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestAPIJSONPreservesPayloads(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"stream envelope", `{"seq":18446744073709551615,"data":{"ts":"2026-10-24T01:02:03-05:00","body":"quoted \"ts\":\"2026-10-24T01:02:03Z\"","goal":null}}`, `{"seq":18446744073709551615,"data":{"ts":"2026-10-24T06:02:03.000000000Z","body":"quoted \"ts\":\"2026-10-24T01:02:03Z\"","goal":null}}`},
		{"timestamp arrays", `{"createdAt":["2026-10-24 01:02:03",["2026-10-24T01:02:03Z"],{"body":"2026-10-24T01:02:03Z"},null,42]}`, `{"createdAt":["2026-10-24T01:02:03.000000000Z",["2026-10-24T01:02:03.000000000Z"],{"body":"2026-10-24T01:02:03Z"},null,42]}`},
		{"escaped keys and values", `{"created\u0041t":"2026-10-24T01:02:03\u005a","body":"<b>\n😀\\</b>"}`, `{"createdAt":"2026-10-24T01:02:03.000000000Z","body":"<b>\n😀\\</b>"}`},
		{"invalid dates", `{"ts":"unknown","timestamp":null,"createdAt":false,"body":"2026-10-24T01:02:03Z"}`, `{"ts":"unknown","timestamp":null,"createdAt":false,"body":"2026-10-24T01:02:03Z"}`},
		{"top level array", `[{"at":"2026-10-24T01:02:03.123Z"},{"at":"2026-10-24T01:02:04Z"}]`, `[{"at":"2026-10-24T01:02:03.123000000Z"},{"at":"2026-10-24T01:02:04.000000000Z"}]`},
		{"scalar", `"2026-10-24T01:02:03Z"`, `"2026-10-24T01:02:03Z"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := marshalAPIJSON(json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			decode := func(data []byte) any {
				var v any
				d := json.NewDecoder(bytes.NewReader(data))
				d.UseNumber()
				if err := d.Decode(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			if !reflect.DeepEqual(decode(got), decode([]byte(tc.want))) {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
	if _, err := marshalAPIJSON(make(chan int)); err == nil {
		t.Fatal("unsupported values must fail")
	}
	if _, err := marshalAPIJSON(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid raw JSON must fail")
	}
}

func FuzzAPIJSONNormalizesOnlyTimestampFields(f *testing.F) {
	for _, seed := range []string{
		`{"seq":1,"data":{"ts":"2026-10-24T01:02:03-05:00","body":"quoted \"ts\":\"2026-10-24T01:02:03Z\""}}`,
		`{"createdAt":["2026-10-24 01:02:03",["2026-10-24T01:02:03Z"],{"body":"2026-10-24T01:02:03Z"},null,42]}`,
		`{"createdAt":"2026-10-24T01:02:03Z","body":"<b>\n😀\\</b>","at":{"at":"2026-10-24T01:02:03Z"}}`,
		`[{"at":"2026-10-24T01:02:03.123Z"},{"timestamp":"x\"y","ts":"2026-10-24T01:02:04Z"}]`,
		`{"items":[{"timestamp":"2026-10-24T01:02:03Z","text":"\\\"timestamp\\\":\\\"2026-10-24T01:02:03Z\\\""}],"since":"2026-10-24T01:02:03+01:00"}`,
		`"2026-10-24T01:02:03Z"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if !json.Valid([]byte(input)) {
			return
		}
		got, err := marshalAPIJSON(json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		decode := func(data []byte) any {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				t.Fatalf("decode %q: %v", data, err)
			}
			return value
		}
		var normalize func(any, string) any
		normalize = func(value any, field string) any {
			switch typed := value.(type) {
			case map[string]any:
				for key, item := range typed {
					typed[key] = normalize(item, key)
				}
			case []any:
				for index, item := range typed {
					typed[index] = normalize(item, field)
				}
			case string:
				if _, ok := apiTimestampFields[field]; ok {
					return normalizeAPITimestamp(typed)
				}
			}
			return value
		}
		if want := normalize(decode([]byte(input)), ""); !reflect.DeepEqual(decode(got), want) {
			t.Fatalf("marshalAPIJSON(%s) = %s", input, got)
		}
	})
}
