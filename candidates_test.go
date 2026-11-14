package main

import (
	"strings"
	"testing"
	"time"
)

func TestUnknownUIStringsAndDiff(t *testing.T) {
	rules := []*Rule{{Kind: "exact", Name: "Known"}}
	js := `label:"Known",title:"New Title",description:"New Description",other:"Ignored",tooltipText:"New Tooltip"`
	got := unknownUIStrings(js, rules)
	want := map[string]bool{"New Title": true, "New Description": true, "New Tooltip": true}
	if len(got) != len(want) {
		t.Fatalf("候选数量=%d, want %d: %#v", len(got), len(want), got)
	}
	for _, v := range got {
		if !want[v] {
			t.Fatalf("意外候选: %q", v)
		}
	}
	if n := newStringCount(got, []string{"New Title"}); n != 2 {
		t.Fatalf("新增数量=%d, want 2", n)
	}
	if n := newStringCount(got, []string{}); n != 3 {
		t.Fatalf("空历史基线应统计全部候选, got %d", n)
	}
	if n := newStringCount(got, nil); n != 0 {
		t.Fatalf("无历史基线时不应提示, got %d", n)
	}
}

func TestUnknownUIStringsPerformance(t *testing.T) {
	rules := []*Rule{{Kind: "exact", Name: "Known"}}
	var b strings.Builder
	line := `label:"New Interface Label 123",title:"Known",description:"Another new description",`
	for b.Len() < 12<<20 {
		b.WriteString(line)
	}
	start := time.Now()
	got := unknownUIStrings(b.String(), rules)
	elapsed := time.Since(start)
	t.Logf("12MB 单遍候选扫描耗时 %s,候选 %d 条", elapsed, len(got))
	if len(got) != 2 {
		t.Fatalf("候选数量=%d, want 2", len(got))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("候选扫描超过 200ms 预算: %s", elapsed)
	}
}
