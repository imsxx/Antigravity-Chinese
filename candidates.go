package main

import (
	"sort"
	"strings"
)

// unknownUIStrings returns the unique UI-looking strings not covered by the
// current dictionary. It is a single-pass scan used only after a version change.
func unknownUIStrings(mainJS string, rules []*Rule) []string {
	known := dictNames(rules)
	seen := map[string]bool{}
	pos := 0
	for {
		rel := strings.Index(mainJS[pos:], `:"`)
		if rel < 0 {
			break
		}
		anchor := pos + rel
		key := keyWordBefore(mainJS, anchor)
		if value, end, ok := quotedValueAt(mainJS, anchor+1); ok {
			if key != "" && keyCategories[key] {
				if !known[value] && !seen[value] && isCandidate(value) {
					seen[value] = true
				}
			}
			pos = end
			continue
		}
		// 未命中的锚点只跳过 :" 两个字符,不能吞掉下一个字符串的起始引号。
		pos = anchor + 2
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func newStringCount(current, previous []string) int {
	// nil 表示没有可用的历史基线;空数组表示上一版确实没有未汉化文案。
	if previous == nil {
		return 0
	}
	old := make(map[string]bool, len(previous))
	for _, v := range previous {
		old[v] = true
	}
	n := 0
	for _, v := range current {
		if !old[v] {
			n++
		}
	}
	return n
}
