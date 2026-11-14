package main

import "testing"

func TestCompareToolVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v2.33.0", "v2.33.0", 0},
		{"2.32.1", "v2.33.0", -1},
		{"v2.33.0", "v2.32.9", 1},
		{"v2.33.0-beta.2", "v2.33.0", -1},
		{"v2.33.0", "v2.33.0-beta.2", 1},
		{"v2.33.0-beta.2", "v2.33.0-beta.10", -1},
		{"v2.33", "v2.33.0", 0},
		{"v2.33.0+build.1", "v2.33.0", 0},
	}
	for _, tt := range tests {
		got, err := compareToolVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("compare(%q,%q): %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Fatalf("compare(%q,%q)=%d,want %d", tt.a, tt.b, got, tt.want)
		}
	}
	if _, err := parseToolVersion("dev"); err == nil {
		t.Fatal("dev 应被视为无效发布版本")
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte("\ufeff" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  设置与维护.exe\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb *antigravity-cn.exe\n" +
		"SHA256 (translations_settings.json) = cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\n")
	sums, err := parseChecksums(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"设置与维护.exe", "antigravity-cn.exe", "translations_settings.json"} {
		if _, err := checksumForAsset(sums, name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := parseChecksums([]byte("bad")); err == nil {
		t.Fatal("无效校验文件应报错")
	}
	if _, err := parseChecksums([]byte(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa a\n" +
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb a\n")); err == nil {
		t.Fatal("冲突校验值应报错")
	}
}
