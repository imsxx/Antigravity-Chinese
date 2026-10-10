// 规则加载与执行:exact / regex / keys / quoted 四种类型。
//
// 匹配原则(与词典约定一致,勿改):
//   - 全部规则都必须完整匹配,不做裸词全局替换;
//   - exact 与 quoted 按名称长度降序执行(长片段优先,避免短片段先替换破坏长片段);
//   - keys 以 ":" 为字面量锚点单遍扫描,只匹配 `键:"..."` 完整形式;
//   - 正则替换串中的 \1 需转换为 Go 的 ${1},字面 $ 需转义为 $$。
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// 命中率低于该阈值时提示客户端可能已更新
const hitRateWarn = 0.8

// Rule 一条翻译规则。
type Rule struct {
	Kind    string // exact / quoted / key:<分类> / regex
	Name    string // 原英文(片段 / 键 / 正则说明)
	Zh      string // 中文
	Pattern string // 原始正则(仅 regex)
	Re      *regexp.Regexp
	Rep     string // 转换为 Go 语法的替换串
}

// keyCategories 键值对可用的属性分类(与 Python 版保持一致)。
var keyCategories = map[string]bool{
	"label": true, "title": true, "description": true, "displayName": true,
	"sectionTitle": true, "shortDescription": true, "longDescription": true,
	"subtitle": true, "text": true, "tooltipText": true, "sidebarLabel": true,
	"searchPlaceholder": true, "tooltip": true,
}

// convertReplacement 把 \1 形式转为 Go 的 ${1},并把字面 $ 转义为 $$。
func convertReplacement(rep string) string {
	var b strings.Builder
	for i := 0; i < len(rep); {
		c := rep[i]
		if c == '\\' && i+1 < len(rep) && rep[i+1] >= '1' && rep[i+1] <= '9' {
			b.WriteString("${")
			b.WriteByte(rep[i+1])
			b.WriteByte('}')
			i += 2
			continue
		}
		if c == '$' {
			b.WriteString("$$")
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// loadRules 读取词典并编译全部规则。
func loadRules(jsonPath string) ([]*Rule, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("未找到词典文件 %s", jsonPath)
	}
	parsed, err := parseOrderedJSON(data)
	if err != nil {
		return nil, fmt.Errorf("词典 JSON 解析失败: %w", err)
	}
	root, ok := parsed.(*jsonObject)
	if !ok {
		return nil, fmt.Errorf("词典结构异常(顶层不是对象)")
	}

	var rules []*Rule
	getStr := func(o *jsonObject, k string) string {
		if v, ok := o.vals[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	// 1) exact_strings
	if v, ok := root.Get("exact_strings"); ok {
		if obj, ok := v.(*jsonObject); ok {
			for _, k := range obj.keys {
				if k == "" {
					continue
				}
				rules = append(rules, &Rule{Kind: "exact", Name: k, Zh: getStr(obj, k)})
			}
		}
	}

	// 2) quoted_strings
	if v, ok := root.Get("quoted_strings"); ok {
		if obj, ok := v.(*jsonObject); ok {
			for _, k := range obj.keys {
				if strings.ContainsAny(k, "\"\\") {
					fmt.Printf("警告:quoted_strings 条目包含引号或反斜杠,已跳过: %q\n", k)
					continue
				}
				rules = append(rules, &Rule{Kind: "quoted", Name: k, Zh: getStr(obj, k)})
			}
		}
	}

	// 3) keys
	if v, ok := root.Get("keys"); ok {
		if obj, ok := v.(*jsonObject); ok {
			for _, cat := range obj.keys {
				if !keyCategories[cat] {
					fmt.Printf("警告:未知的 keys 分类 %q,已跳过。\n", cat)
					continue
				}
				tbl, ok := obj.vals[cat].(*jsonObject)
				if !ok {
					continue
				}
				for _, k := range tbl.keys {
					if strings.Contains(k, "\"") {
						fmt.Printf("警告:keys.%s 条目包含双引号,已跳过: %q\n", cat, k)
						continue
					}
					rules = append(rules, &Rule{Kind: "key:" + cat, Name: k, Zh: getStr(tbl, k)})
				}
			}
		}
	}

	// 4) regex
	if v, ok := root.Get("regex"); ok {
		arr, ok := v.([]interface{})
		if !ok {
			return nil, fmt.Errorf("regex 字段不是数组")
		}
		for _, item := range arr {
			obj, ok := item.(*jsonObject)
			if !ok {
				continue
			}
			pat := getStr(obj, "pattern")
			rep := getStr(obj, "replace")
			note := getStr(obj, "note")
			if note == "" {
				note = truncateRunes(pat, 60)
			}
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("正则规则编译失败(%s): %v\n  模式: %s", note, err, pat)
			}
			rules = append(rules, &Rule{Kind: "regex", Name: note, Pattern: pat, Re: re, Rep: convertReplacement(rep)})
		}
	}

	if len(rules) == 0 {
		return nil, fmt.Errorf("词典为空或格式不正确")
	}
	return rules, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// applyRules 依次执行 exact -> regex -> keys -> quoted,返回新文本与每条规则的命中次数。
func applyRules(text string, rules []*Rule) (string, map[*Rule]int) {
	hit := make(map[*Rule]int, len(rules))
	var exact, quoted, regexRules []*Rule
	kmap := map[string]map[string]*Rule{}

	for _, r := range rules {
		switch {
		case r.Kind == "exact":
			exact = append(exact, r)
		case r.Kind == "quoted":
			quoted = append(quoted, r)
		case r.Kind == "regex":
			regexRules = append(regexRules, r)
		default:
			cat := strings.TrimPrefix(r.Kind, "key:")
			if kmap[cat] == nil {
				kmap[cat] = map[string]*Rule{}
			}
			kmap[cat][r.Name] = r
		}
	}

	// 1) 完整片段替换(长片段优先)
	sort.SliceStable(exact, func(i, j int) bool { return len(exact[i].Name) > len(exact[j].Name) })
	for _, r := range exact {
		if n := strings.Count(text, r.Name); n > 0 {
			text = strings.ReplaceAll(text, r.Name, r.Zh)
			hit[r] = n
		}
	}

	// 2) 正则
	for _, r := range regexRules {
		if m := r.Re.FindAllStringIndex(text, -1); len(m) > 0 {
			text = r.Re.ReplaceAllString(text, r.Rep)
			hit[r] = len(m)
		}
	}

	// 3) 键值对扫描
	text = applyKeyedRules(text, kmap, hit)

	// 4) 带引号字符串(长条目优先)
	sort.SliceStable(quoted, func(i, j int) bool { return len(quoted[i].Name) > len(quoted[j].Name) })
	for _, r := range quoted {
		needle := `"` + r.Name + `"`
		if n := strings.Count(text, needle); n > 0 {
			text = strings.ReplaceAll(text, needle, `"`+r.Zh+`"`)
			hit[r] = n
		}
	}

	return text, hit
}

// applyKeyedRules 以 ":" 为锚点单遍扫描,只替换 `键:"值"` 完整形式。
// 未命中的锚点只跳过「:"」两个字符,绝不回吞后续内容。
func applyKeyedRules(text string, kmap map[string]map[string]*Rule, hit map[*Rule]int) string {
	var b strings.Builder
	b.Grow(len(text) + 4096)
	pos := 0
	for {
		rel := strings.Index(text[pos:], `:"`)
		if rel < 0 {
			b.WriteString(text[pos:])
			break
		}
		i := pos + rel
		if word := keyWordBefore(text, i); word != "" {
			if tbl := kmap[word]; tbl != nil {
				if val, end, ok := quotedValueAt(text, i+1); ok {
					if r := tbl[val]; r != nil {
						b.WriteString(text[pos : i+1])
						b.WriteString(`"` + r.Zh + `"`)
						hit[r]++
						pos = end
						continue
					}
				}
			}
		}
		b.WriteString(text[pos : i+2])
		pos = i + 2
	}
	return b.String()
}

// keyWordBefore 取 ":" 前紧邻的标识符(等价于正则 [A-Za-z_$][\w$]*\z 在 128 字符窗口内的搜索)。
func keyWordBefore(text string, i int) string {
	start := i - 128
	if start < 0 {
		start = 0
	}
	win := text[start:i]
	j := len(win)
	for j > 0 && isWordOrDollar(win[j-1]) {
		j--
	}
	for j < len(win) && !isIdentStart(win[j]) {
		j++
	}
	if j >= len(win) {
		return ""
	}
	return win[j:]
}

// quotedValueAt 从 text[i] 的引号起解析一个字符串字面量,返回(转义还原后的内容, 结束位置, 是否成功)。
func quotedValueAt(text string, i int) (string, int, bool) {
	if i >= len(text) || text[i] != '"' {
		return "", 0, false
	}
	for j := i + 1; j < len(text); {
		c := text[j]
		if c == '\\' {
			j += 2
			continue
		}
		if c == '"' {
			inner := text[i+1 : j]
			return strings.ReplaceAll(inner, `\"`, `"`), j + 1, true
		}
		j++
	}
	return "", 0, false
}

func isWordOrDollar(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// reportStats 打印命中统计;命中率偏低时给出警告。
func reportStats(rules []*Rule, hit map[*Rule]int) (ok, total, repl int) {
	total = len(rules)
	var missed []*Rule
	for _, r := range rules {
		if n := hit[r]; n > 0 {
			ok++
			repl += n
		} else {
			missed = append(missed, r)
		}
	}
	fmt.Printf("翻译规则命中: %d/%d 条,合计替换 %d 处\n", ok, total, repl)
	if len(missed) > 0 {
		fmt.Printf("未命中规则(%d 条):\n", len(missed))
		for i, r := range missed {
			if i >= 15 {
				break
			}
			fmt.Printf("  [%s] %s\n", r.Kind, truncateRunes(r.Name, 90))
		}
		if len(missed) > 15 {
			fmt.Printf("  ... 其余 %d 条详见 --scan 报告\n", len(missed)-15)
		}
	}
	if total > 0 {
		rate := float64(ok) / float64(total)
		if rate < hitRateWarn {
			fmt.Println("")
			fmt.Println(strings.Repeat("*", 60))
			fmt.Printf("警告:规则命中率偏低(%.0f%%),客户端可能已更新。\n", rate*100)
			fmt.Println("建议运行: antigravity-chinese.exe --scan 检查新增/变更文案。")
			fmt.Println(strings.Repeat("*", 60))
		}
	}
	return ok, total, repl
}

// dictNames 收集非正则规则名(用于 --scan 候选过滤)。
func dictNames(rules []*Rule) map[string]bool {
	m := map[string]bool{}
	for _, r := range rules {
		if r.Kind != "regex" {
			m[r.Name] = true
		}
	}
	return m
}
