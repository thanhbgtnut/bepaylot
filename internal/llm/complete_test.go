package llm

import "testing"

func TestDecodeJSON(t *testing.T) {
	var v struct {
		Select []string `json:"select"`
	}
	cases := []string{
		`{"select":["n1","n2"]}`,
		"Here you go:\n```json\n{\"select\": [\"n1\", \"n2\"]}\n```",
		`Sure. {"select":["n1","n2"]} Hope this helps {x}`,
	}
	for _, c := range cases {
		v.Select = nil
		if err := DecodeJSON(c, &v); err != nil || len(v.Select) != 2 {
			t.Fatalf("%q: %v %v", c, v, err)
		}
	}
	if err := DecodeJSON("no json here", &v); err == nil {
		t.Fatal("want error")
	}
	var s struct{ A string }
	if err := DecodeJSON(`{"A":"a } { b"}`, &s); err != nil || s.A != "a } { b" {
		t.Fatalf("braces in string: %v %v", s, err)
	}
}
