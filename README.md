# Antigravity-Chinese (Antigravity 设置界面一键中文化工具)

专为 **Google Antigravity** 客户端打造的设置界面（`Ctrl + ,`）轻量级一键中文化工具。

> 解决官方客户端频繁自动更新后界面重置回英文的痛点，每次更新后只需双击运行一次，即可秒级恢复中文设置界面。

> **非官方项目**：本项目与 Google 无任何关联，未经 Google 认可或授权，不代表 Google。请仅用于个人本地界面汉化。

---

## 📌 当前支持版本

- **当前验证版本**：`v2.22.0`（通过动态提取机制，可兼容后续版本更新）
- **兼容性自检**：官方更新后先运行扫描、再干跑验证，确认命中率与新增文案，无需盲目试错（exe 菜单里直接选 3 / 4）
- **支持操作系统**：Windows 10 / 11（支持自定义安装路径）
- **零依赖**：工具为 Go 编译的原生单文件程序（约 3MB），**无需安装 Python 或任何运行时**，下载即用

---

## 🌟 核心特性

- 🎯 **全景深度汉化**：
  - **通用 (General)**：执行策略、排队消息时机、安全预设、制品审核策略、浏览器自动化权限及详细说明。
  - **应用 (Application)**：常驻菜单栏、远程控制 (Remote Control)、通知系统跳转、客户端版本。
  - **外观 (Appearance)**：主题切换（跟随系统/浅色/深色）、主题调色板（预设选择、背景色、前景色、强调色）。
  - **模型与配额 (Models & Usage)**：当前套餐展示、升级套餐入口、AI 额度超额开关、Gemini/Claude/GPT 模型每周及5小时配额卡片与刷新时间动态翻译。
  - **自定义 (Customizations)**：Token 消耗明细、技能（Skills 全局徽标）、规则（Rules）、扩展插件（Plugins）、已安装的 MCP 服务器管理与配置文件入口。
  - **侧边栏与面板**：新建对话、对话历史、自动化/计划任务、UI 插件、自定义、设置、提供反馈及其悬停提示。
  - **交互元素**：所有下拉选择框（Dropdowns）、按钮（打开、编辑、添加、保存、取消、删除、重置等）、鼠标悬停浮窗（Hover Tooltips）、展开器（See all / See more）及组织策略提示。
- 🖱️ **双击即用（无脚本）**：双击 `antigravity-chinese.exe` 会显示交互菜单（应用 / 还原 / 扫描 / 验证），全程可视；
  从终端调用时保持纯命令行行为，不影响自动化脚本。
- 🛡️ **安全无损与一键还原**：
  - 首次运行会自动在客户端资源目录生成纯净的官方备份：`app.asar.bak`。
  - 采用官方原生的 `--web_bundle_path` 外挂界面技术，**完全不修改任何核心业务逻辑、网络通信与模型交互**。
  - 提供一键还原（菜单选 2，或 `--restore`），随时随地无损回到官方英文原版。
- 🔄 **更新无缝衔接**：
  - 当 Antigravity 自动更新覆盖导致界面回到英文时，只需重新双击运行一次 exe 即可重新注入中文。
- 📝 **词典开源可配**：
  - 附带 `translations_settings.json` 词典文件，任何人都可根据习惯用记事本随时自定义调整翻译文案。

---

## 🚀 使用指南

### 1. 安装与应用中文

1. **完全退出 Antigravity 客户端**（建议在系统托盘右键退出或确保没有残留进程）。
2. 从 [Releases 页面](https://github.com/imsxx/Antigravity-Chinese/releases) 下载最新压缩包并解压
   （仓库克隆用户请先按下方“从源码构建”编译出 `antigravity-chinese.exe`）。
3. **双击运行 `antigravity-chinese.exe`**，在菜单中选择 **`1. 应用中文补丁`**。
4. 提示汉化成功后，重新打开 Antigravity，按下快捷键 **`Ctrl + ,`**，即可体验全中文设置界面！

> 注：无需安装 Python，也无需任何脚本；`translations_settings.json` 需与 exe 放在同一目录。

### 2. 客户端更新后如何恢复？

每次官方客户端自动更新后：双击 `antigravity-chinese.exe` → 选 `1. 应用中文补丁`，数秒内即可重新生成并挂载中文界面。

### 3. 如何还原为官方原版英文？

双击 `antigravity-chinese.exe` → 选 `2. 还原官方英文`；或命令行执行 `antigravity-chinese.exe --restore`。
工具会优先“逆向移除注入”（不依赖备份），立即恢复纯正官方英文。

---

## 📁 目录文件介绍

```text
Antigravity-Chinese/
├── antigravity-chinese.exe      # 单文件工具（发布包内提供；仓库不含二进制）
├── translations_settings.json   # 翻译词典（可自由调整，需与 exe 同目录）
├── main.go                      # Go 引擎入口（菜单/应用/还原/验证 + Win32 集成）
├── asar.go                      # asar 读写（Chromium Pickle + 保序 JSON 重建）
├── rules.go                     # 规则加载与执行（词典驱动，四种匹配类型）
├── scan.go                      # 兼容性扫描报告
├── patch_settings.py            # （参考实现）Python 版引擎，用于交叉验证
└── README.md                    # 说明文档
```

---

## 💡 命令行参数（高级）

```bash
# 双击运行 → 交互菜单（1 应用 / 2 还原 / 3 扫描 / 4 验证）
antigravity-chinese.exe

# 终端 / 脚本用法：
antigravity-chinese.exe --apply              # 应用中文补丁（显式）
antigravity-chinese.exe --restore            # 还原为官方英文（优先逆向移除注入）
antigravity-chinese.exe --scan               # 兼容性扫描：命中率、失效规则、未汉化新增文案
antigravity-chinese.exe --verify             # 干跑验证：只统计命中情况，不写入客户端
antigravity-chinese.exe --path "D:\Your\Path\To\antigravity"   # 指定安装目录
antigravity-chinese.exe --menu               # 强制显示交互菜单
```

### 从源码构建（贡献者）

```bash
# 需要 Go 1.21 及以上版本
go build -trimpath -ldflags "-s -w" -o antigravity-chinese.exe .
```

构建产物为单个静态 exe（约 3MB），无任何外部依赖。

---

## 🚫 使用红线

本项目只做一件事：**翻译界面文案**。以下行为不符合项目定位，也不会被接受：

- 不触碰账号、登录、授权、配额、计费及任何业务逻辑或默认行为；
- 不提交、不打包、不分发任何从客户端提取的产物（如 `web_bundle.zip`、`app.asar`、提取出的界面代码）；
- 请勿将本工具包装为"官方中文版"，或使用 Google / Antigravity 的官方标识。

---

## ⚖️ 免责声明

本工具为非官方项目，与 Google 无任何关联。仅用于个人本地界面汉化学习与体验优化，不包含任何破解、绕过验证或篡改业务逻辑的代码，并提供一键还原。使用前请知悉：修改客户端本地资源可能违反其服务条款，请自行评估并承担相关风险；本项目不对使用本工具产生的其他衍生问题负责。
