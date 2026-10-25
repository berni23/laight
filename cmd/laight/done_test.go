package main

import (
	"fmt"
	"testing"
)

func TestParseOut(t *testing.T) {
	out, err := parseOut([]string{`n=42`, `ok=true`, `none=null`, `list=["a","b"]`, `word=plain`, `stray=["a"] ]`, `two=["a"] ["b"]`})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"n": "42", "ok": "true", "none": "", "list": "[a b]", "word": "plain",
		"stray": `["a"] ]`, "two": `["a"] ["b"]`, // not one JSON value: kept as the string typed
	}
	for k, w := range want {
		if got := fmt.Sprint(out[k]); got != w {
			t.Errorf("%s = %s, want %s", k, got, w)
		}
	}
}
