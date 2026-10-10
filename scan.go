// --scan 兼容性扫描:输出命中率、失效规则、设置项注册表与未汉化候选。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	setupEntryRe = regexp.MustCompile(`\[([A-Za-z]{1,3}\.[A-Z0-9_]+),\{`)
	labelRe      = regexp.MustCompile(`label:"((?:[^"\\]|\\.)*)"`)
	descRe       = regexp.MustCompile("description:(?:'((?:[^'\\\\]|\\\\.)*)'|\"((?:[^\"\\\\]|\\\\.)*)\"|`([^`]*)`)")
	screenRe     = regexp.MustCompile(`screen:"([^"]+)"`)
	wordPairRe   = regexp.MustCompile(`[A-Za-z]{2}`)
)

// isCandidate 与 Python 版规则一致:2~200 字符、含至少两个连续字母、不含代码特征。
func isCandidate(v string) bool {
	n := utf8.RuneCountInString(v)
	if n < 2 || n > 200 {
		return false
	}
	if !wordPairRe.MatchString(v) {
		return false
	}
	for _, bad := range []string{"{", "}", "${", "=>", "://", `\n`, `\u`} {
		if strings.Contains(v, bad) {
			return false
		}
	}
	return true
}

type mineSpec struct {
	key   string
	re    *regexp.Regexp
	guard bool // 需要前导词边界(避免把 tooltipText 里的 text 也挖出来)
}

func mineSpecs() []mineSpec {
	pat := `:"((?:[^"\\]|\\.)*)"`
	specs := []mineSpec{}
	for _, k := range []string{"label", "title", "description", "displayName", "sectionTitle", "shortDescription"} {
		specs = append(specs, mineSpec{key: k, re: regexp.MustCompile(k + pat)})
	}
	for _, k := range []string{"text", "tooltipText", "sidebarLabel", "searchPlaceholder", "tooltip"} {
		specs = append(specs, mineSpec{key: k, re: regexp.MustCompile(k + pat), guard: true})
	}
	return specs
}

func doScan(appDir, exeDir string, rules []*Rule) error {
	var report []string
	w := func(s string) { report = append(report, s) }

	ver := getFileVersion(filepath.Join(appDir, "Antigravity.exe"))
	if ver == "" {
		ver = "未知"
	}
	mainJS, err := readMainJS(appDir)
	if err != nil {
		return err
	}
	fmt.Printf("客户端版本: %s\n", ver)
	fmt.Printf("main.js 大小: %d 字符\n", utf8.RuneCountInString(mainJS))

	w("# Antigravity 汉化兼容性扫描报告")
	w("")
	w(fmt.Sprintf("[客户端版本] %s", ver))
	w(fmt.Sprintf("[main.js 大小] %d 字符", utf8.RuneCountInString(mainJS)))

	_, hit := applyRules(mainJS, rules)
	ok, total, _ := reportStats(rules, hit)
	w("")
	w(fmt.Sprintf("[规则命中] %d/%d 条", ok, total))
	w("")
	w("=== 未命中规则(可能已失效,需适配新版本) ===")
	for _, r := range rules {
		if hit[r] == 0 {
			w(fmt.Sprintf("  [%s] %s", r.Kind, truncateRunes(r.Name, 150)))
		}
	}

	// 设置项注册表(screen / 标识 / label / description)
	w("")
	w("=== 设置项注册表(screen / 标识 / label / description) ===")
	seen := map[string]bool{}
	for _, m := range setupEntryRe.FindAllStringSubmatchIndex(mainJS, -1) {
		ident := mainJS[m[2]:m[3]]
		end := m[0] + 1200
		if end > len(mainJS) {
			end = len(mainJS)
		}
		seg := mainJS[m[0]:end]
		label := ""
		if lm := labelRe.FindStringSubmatch(seg); lm != nil {
			label = lm[1]
		}
		desc := ""
		if dm := descRe.FindStringSubmatch(seg); dm != nil {
			for i := 1; i <= 3; i++ {
				if dm[i] != "" {
					desc = dm[i]
					break
				}
			}
		}
		screen := ""
		if sm := screenRe.FindStringSubmatch(seg); sm != nil {
			screen = sm[1]
		}
		key := ident + "\x00" + label
		if seen[key] {
			continue
		}
		seen[key] = true
		if screen == "" {
			screen = "?"
		}
		w(fmt.Sprintf("  [%s] %s", screen, ident))
		if label != "" {
			w("      label: " + label)
		}
		if desc != "" {
			w("      desc : " + truncateRunes(strings.ReplaceAll(desc, "\n", " "), 160))
		}
	}

	// 未汉化候选(不在词典中的界面文案)
	known := dictNames(rules)
	w("")
	w("=== 未汉化候选(不在词典中的界面文案) ===")
	for _, sp := range mineSpecs() {
		counts := map[string]int{}
		for _, m := range sp.re.FindAllStringSubmatchIndex(mainJS, -1) {
			if sp.guard && m[0] > 0 && isWordOrDollar(mainJS[m[0]-1]) {
				continue
			}
			counts[mainJS[m[2]:m[3]]]++
		}
		var cand []string
		for v := range counts {
			if !known[v] && isCandidate(v) {
				cand = append(cand, v)
			}
		}
		sort.Strings(cand)
		w("")
		w(fmt.Sprintf("--- %s(%d 条)---", sp.key, len(cand)))
		for _, v := range cand {
			w(fmt.Sprintf("  x%d  %s", counts[v], v))
		}
	}

	// app.asar 结构自检
	w("")
	w("=== app.asar 结构自检 ===")
	asarPath := filepath.Join(appDir, "resources", "app.asar")
	if _, files, err := unpackAsar(asarPath); err != nil {
		w(fmt.Sprintf("  !! app.asar 解析失败: %v", err))
	} else if ls, ok := files[lsScriptPath]; !ok {
		w(fmt.Sprintf("  !! 未找到 %s,asar 结构可能已变化", lsScriptPath))
	} else {
		code := toValidUTF8(ls)
		w(fmt.Sprintf("  注入锚点命中: %s", yesNo(strings.Contains(code, insertAfter))))
		w(fmt.Sprintf("  当前是否已打补丁: %s", yesNo(strings.Contains(code, patchMarker))))
	}

	outPath := filepath.Join(exeDir, "scan_report.txt")
	if err := os.WriteFile(outPath, []byte(strings.Join(report, "\n")), 0o644); err != nil {
		return err
	}
	fmt.Println("")
	fmt.Printf("扫描完成,完整报告已写入: %s\n", outPath)
	return nil
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}
