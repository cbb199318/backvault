package main

import (
	"encoding/json"
	"testing"
)

func TestCmpVer(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.4.10", "0.4.9", 1}, {"0.4.4", "0.4.4", 0}, {"v1.0.0", "0.9.9", 1},
		{"0.4.4-beta", "0.4.4", 0}, {"0.4", "0.4.0", 0}, {"0.3.11", "0.4.0", -1},
	}
	for _, c := range cases {
		if got := cmpVer(c.a, c.b); got != c.want {
			t.Errorf("cmpVer(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestMigrateFromLegacy(t *testing.T) {
	old := []byte(`{"repos":[{"id":"r1","future_field":"keep"}],"tasks":[]}`)
	out, changed, err := migrateConfigBytes(old)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if int(m["schema"].(float64)) != currentSchema() {
		t.Fatalf("schema=%v", m["schema"])
	}
	if m["repos"].([]any)[0].(map[string]any)["future_field"] != "keep" {
		t.Fatal("unknown fields must survive migration")
	}
	// 幂等：再跑一次不应变化
	if _, changed, _ := migrateConfigBytes(out); changed {
		t.Fatal("second run should be a no-op")
	}
	// 回滚到老程序读到更高 schema：原样返回
	if _, changed, _ := migrateConfigBytes([]byte(`{"schema":999}`)); changed {
		t.Fatal("newer schema must be left alone")
	}
}
