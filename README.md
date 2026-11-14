# Antigravity-Chinese(Antigravity 界面中文化)

专为 **Google Antigravity** 客户端打造的轻量界面汉化工具,覆盖设置页、主侧栏、对话操作、输入框与常用交互文案。

> **非官方项目**:本项目与 Google 无任何关联,未经 Google 认可或授权。请仅用于个人本地界面汉化。

---

## 当前支持

- **工具版本**:`v2.33.1`
- **已验证客户端**:Antigravity `2.32.1`
- **覆盖规模**:`1504` 条翻译规则,`2042` 处实际替换(全部命中)
- **平台**:Windows 10 / 11 当前用户,无需管理员权限
- **零第三方依赖**:Go 标准库原生程序,无需 Python、Node.js 或任何运行时

---

## 推荐入口:Antigravity CN

发布包中现在有两个程序:

- `antigravity-cn.exe` — **推荐入口**,终端启动器;
- `设置与维护.exe` — 控制台维护工具,用于还原、扫描、验证和修复。

双击 `antigravity-cn.exe` 后会打开一个终端窗口,并自动:

1. 检查本项目的稳定版 Release;
2. 检查本地补丁状态(客户端版本、asar 注入标记、资源包完整性和词典哈希);
3. 需要时安全应用中文补丁;
4. 启动 Antigravity;
5. 显示“运行完成,将在 3 秒后自动关闭本窗口”的倒计时。

所有检查步骤都会保留在终端里,不会互相覆盖。遇到更新提示或失败操作时,使用 `↑` / `↓` 选择,按 `Enter` 确认。首次成功汉化后会创建当前用户桌面的 `Antigravity CN` 快捷方式。以后从该快捷方式启动即可。工具不会复制第二套客户端,也不会在后台常驻。

启动器使用独立的 `Antigravity CN` 图标;终端窗口会在屏幕中央打开。正常流程结束后不会保留交互窗口,而是显示 3 秒倒计时后自动退出。

### 更新提示

发现本项目新版本时,启动器会显示三个选项:

- **立即更新**:下载并校验新版本,自动替换两个程序和词典;
- **不更新启动**:只跳过本次,下次仍会检查;
- **关闭更新检查**:写入配置并立即继续启动。之后可在控制台菜单中重新开启。

更新失败会回滚并保留旧版本。旧词典会备份为 `translations_settings.json.bak`。更新检查默认开启,只访问本项目 GitHub Release,不发送遥测、账号或客户端数据。

> 更新检查针对的是**汉化工具**,不是 Antigravity 客户端。客户端更新仍由官方客户端自行完成。
>
> 从 `v2.33.0` 升级到本版时,由于维护工具更名,旧版启动器无法自动完成这一次更新,请手动下载压缩包覆盖一次;从 `v2.33.1` 起自动更新恢复正常。

---

## 快速使用

1. 完全退出 Antigravity(包括托盘进程);
2. 从 [Releases](https://github.com/imsxx/Antigravity-Chinese/releases) 下载最新压缩包并解压到固定目录;
3. 双击 `antigravity-cn.exe`;
4. 等待终端显示“运行完成”,客户端会自动打开;
5. 按 `Ctrl + ,` 查看中文设置界面。

如果检测到客户端正在运行,启动器会提示先关闭客户端并提供“关闭并重试 / 取消”。它不会强制结束进程。

### 客户端更新后

官方客户端更新并重启后,界面可能暂时恢复英文。此时不需要重新下载工具:

**关闭客户端 → 从 `Antigravity CN` 快捷方式启动 → 自动重新汉化并打开。**

如果新版增加了尚未收录的界面文案,启动器会在重新汉化时提示:

> 提示:新版中有 12 条界面文案暂未汉化,已按原英文显示。

该统计使用单遍属性扫描,只在新版重新汉化时执行;超过约 200ms 或统计失败时不会弹出提示,不影响启动。

---

## 还原与维护

双击 `设置与维护.exe` 打开维护菜单:

```text
1. 应用中文补丁
2. 还原官方英文
3. 兼容性扫描(适配新版)
4. 干跑验证
5. 创建/修复 Antigravity CN 桌面快捷方式
6. 关闭 / 重新开启自动更新检查
```

还原优先“逆向移除注入”,不依赖备份。所有写入都会先生成临时文件、完成回读校验,再事务替换;失败时保留原客户端。

---

## 状态与配置

数据目录:

```text
%LOCALAPPDATA%\Antigravity-Chinese\
├── config.json        # updateCheckEnabled
├── state.json         # 最近汉化版本、词典/资源包哈希、未汉化文案基线
├── update-error.txt   # 上次自更新失败原因,下次启动时显示后清除
└── logs\launcher.log  # 轮转日志
```

正常启动不显示日志路径;失败时可在终端菜单选择“打开日志”。所有错误和警告都在当前终端内用颜色区分,不会另外弹出提示框。

---

## 命令行

```powershell
设置与维护.exe                    # 双击时进入菜单;终端调用默认应用补丁
设置与维护.exe --apply            # 应用中文补丁
设置与维护.exe --restore          # 还原官方英文
设置与维护.exe --scan             # 兼容性扫描,生成 scan_report.txt
设置与维护.exe --verify           # 干跑验证,不写入客户端
设置与维护.exe --status           # 输出补丁状态
设置与维护.exe --status --json    # 机器可读状态
设置与维护.exe --version          # 输出版本号
设置与维护.exe --create-shortcut  # 创建/修复桌面快捷方式
设置与维护.exe --enable-update-check
设置与维护.exe --disable-update-check
设置与维护.exe --path "D:\Your\Path\To\antigravity"
```

`--status` 退出码:

- `0`:补丁已就绪;
- `3`:需要汉化;
- `1`:检查出错。

---

## 目录文件

```text
Antigravity-Chinese/
├── antigravity-cn.exe           # 终端启动器(发布包提供)
├── 设置与维护.exe                # 控制台维护工具(发布包提供)
├── assets/antigravity-cn.ico    # 启动器多尺寸图标
├── rsrc_windows_amd64.syso      # 由图标生成,构建时自动嵌入 exe
├── translations_settings.json   # 翻译词典
├── launcher_main.go             # 启动器主流程
├── launcher_ui.go               # 终端日志、颜色与上下键菜单
├── update.go                    # Release 检测、下载、校验与自更新
├── main.go                      # 汉化/还原引擎
├── asar.go                      # asar 读写(Chromium Pickle + 保序 JSON)
├── rules.go                     # 词典规则执行
├── status.go                    # 本地补丁状态检查
├── candidates.go                # 新增未汉化文案单遍扫描
├── shortcut.go                  # 原生 COM 桌面快捷方式
├── scan.go                      # 兼容性扫描报告
├── patch_settings.py            # Python 参考实现(交叉验证基准)
└── README.md
```

---

## 从源码构建

需要 Go 1.21 及以上版本:

```powershell
go test ./...
go build -trimpath -ldflags "-s -w -X main.toolVersion=v2.33.1" -o '设置与维护.exe' .
go build -trimpath -tags launcher -ldflags "-s -w -X main.toolVersion=v2.33.1" -o antigravity-cn.exe .
```

发布流水线会在推送 `v*` tag 时自动执行:

- `go vet ./...`、`go vet -tags launcher ./...`、`go test ./...`;
- 生成 `设置与维护.exe`、`antigravity-cn.exe`、压缩包和 `checksums.txt`;
- 上传压缩包、两个 exe 与校验文件;
- Release 说明自动引用 `CHANGELOG.md` 对应版本。

---

## 安全边界

- 只翻译界面文案,不碰账号、登录、授权、配额、计费、遥测默认值或任何业务逻辑;
- 应用前执行 asar“解包 → 重打包”逐字节自检;锚点或结构变化时中止,不写入客户端;
- `app.asar` 与 `web_bundle.zip` 先写临时文件、完整回读校验,再事务替换并支持回滚;
- 不复制第二套客户端,不注入后台守护进程,不强制结束 Antigravity;
- 不提交、不打包任何从客户端提取的产物(`app.asar`、`web_bundle.zip`、`main.js` 等);
- 二进制只通过 GitHub Releases 分发,仓库不保存 exe。

---

## 免责声明

本工具为非官方项目,与 Google 无任何关联。仅用于个人本地界面汉化与体验优化,不包含破解、绕过验证或篡改业务逻辑的代码,并提供一键还原。修改客户端本地资源可能违反其服务条款,请自行评估并承担相关风险。
