//go:build launcher

package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestMenuSelectionWraps(t *testing.T) {
	tests := []struct {
		name    string
		current int
		delta   int
		count   int
		want    int
	}{
		{name: "向下", current: 0, delta: 1, count: 3, want: 1},
		{name: "向下到底回绕", current: 2, delta: 1, count: 3, want: 0},
		{name: "向上", current: 1, delta: -1, count: 3, want: 0},
		{name: "向上到顶回绕", current: 0, delta: -1, count: 3, want: 2},
		{name: "空菜单", current: 0, delta: 1, count: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := menuSelection(tt.current, tt.delta, tt.count); got != tt.want {
				t.Fatalf("menuSelection(%d, %d, %d) = %d, 期望 %d",
					tt.current, tt.delta, tt.count, got, tt.want)
			}
		})
	}
}

func TestSelectionForCloseAction(t *testing.T) {
	buttons := []buttonSpec{
		{ID: 1, Text: "立即更新"},
		{ID: 2, Text: "不更新启动"},
		{ID: 3, Text: "关闭更新检查"},
	}
	if got := selectionForCloseAction(buttons, 2); got != 1 {
		t.Fatalf("关闭动作应选中第 2 项，实际 %d", got)
	}
	if got := selectionForCloseAction(buttons, 99); got != len(buttons)-1 {
		t.Fatalf("未知关闭动作应回退到最后一项，实际 %d", got)
	}
}

func TestDetailLinesPreserveEveryStep(t *testing.T) {
	got := detailLines("正在检查本地补丁\r\n\r\n正在等待工具更新检查…  \n补丁已就绪")
	want := []string{"正在检查本地补丁", "正在等待工具更新检查…", "补丁已就绪"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detailLines = %q, 期望 %q", got, want)
	}
}

func TestTerminalKeepsCompletedSteps(t *testing.T) {
	var output bytes.Buffer
	ui := &launcherUI{writer: &output}
	ui.showBusy("正在检查启动环境", "正在检查本地补丁", 0, false)
	ui.showBusy("正在检查启动环境", "正在等待工具更新检查…", 20, false)
	ui.showBusy("正在检查启动环境", "更新检查已完成", 30, false)

	text := output.String()
	for _, step := range []string{"正在检查本地补丁", "正在等待工具更新检查…", "更新检查已完成"} {
		if !strings.Contains(text, step) {
			t.Fatalf("终端输出缺少步骤 %q: %q", step, text)
		}
	}
	if strings.Count(text, "[正在检查启动环境]") != 1 {
		t.Fatalf("同阶段标题不应重复: %q", text)
	}
}

func TestTerminalPreservesFinalDownloadProgress(t *testing.T) {
	var output bytes.Buffer
	ui := &launcherUI{writer: &output}
	ui.showBusy("正在更新汉化工具", "正在下载新版本…", 5, false)
	ui.updateProgress("正在更新汉化工具", "正在下载新版本… 3.0 / 6.0 MB", 50)
	ui.updateProgress("正在更新汉化工具", "正在下载新版本… 6.0 / 6.0 MB", 78)
	ui.showBusy("正在更新汉化工具", "正在校验更新包…", 80, false)

	text := output.String()
	for _, step := range []string{"正在下载新版本… 6.0 / 6.0 MB", "正在校验更新包…"} {
		if !strings.Contains(text, step) {
			t.Fatalf("终端输出未保留步骤 %q: %q", step, text)
		}
	}
}

func TestTerminalColorsDistinguishSeverity(t *testing.T) {
	var output bytes.Buffer
	ui := &launcherUI{writer: &output, vtEnabled: true}
	ui.warn("更新检查已关闭")
	ui.reportError("无法写入客户端")

	text := output.String()
	if !strings.Contains(text, ansiYellow) || !strings.Contains(text, "更新检查已关闭") {
		t.Fatalf("警告未使用黄色输出: %q", text)
	}
	if !strings.Contains(text, ansiRed) || !strings.Contains(text, "无法写入客户端") {
		t.Fatalf("错误未使用红色输出: %q", text)
	}
	if !strings.Contains(text, ansiReset) {
		t.Fatalf("颜色输出缺少 reset: %q", text)
	}
}

func TestErrorAndWarningTextClassification(t *testing.T) {
	for _, text := range []string{"错误：无法读取", "汉化失败", "检查出现问题", "无法启动客户端"} {
		if color := detailColor(text, ""); color != ansiRed {
			t.Fatalf("%q 应为红色，实际 %q", text, color)
		}
	}
	for _, text := range []string{"提示：新版未汉化文案", "本次已跳过工具更新", "更新检查已关闭", "更新检查未完成"} {
		if color := detailColor(text, ""); color != ansiYellow {
			t.Fatalf("%q 应为黄色，实际 %q", text, color)
		}
	}
	if color := detailColor("Antigravity 已启动", "运行完成"); color != ansiGreen {
		t.Fatalf("完成信息应为绿色，实际 %q", color)
	}
}

func TestCountdownFinishesSuccessfulRun(t *testing.T) {
	var output bytes.Buffer
	ui := &launcherUI{writer: &output, vtEnabled: true}
	ui.countdownAndFinish(1)

	text := output.String()
	for _, want := range []string{"运行完成，将在 1 秒后自动关闭本窗口", "运行完成，窗口即将关闭。"} {
		if !strings.Contains(text, want) {
			t.Fatalf("倒计时输出缺少 %q: %q", want, text)
		}
	}
	if code := ui.exitCodeValue(); code != 0 {
		t.Fatalf("完成流程退出码 = %d, 期望 0", code)
	}
}

func TestCenteredConsolePosition(t *testing.T) {
	work := winRect{Left: 100, Top: 50, Right: 2020, Bottom: 1090}
	x, y := centeredConsolePosition(work, 900, 600)
	if x != 610 || y != 270 {
		t.Fatalf("窗口位置 = (%d, %d), 期望 (610, 270)", x, y)
	}

	x, y = centeredConsolePosition(work, 2400, 1200)
	if x != 100 || y != 50 {
		t.Fatalf("超大窗口应贴住工作区左上角，实际 (%d, %d)", x, y)
	}
}
