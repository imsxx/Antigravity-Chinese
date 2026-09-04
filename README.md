# Antigravity-Chinese (Antigravity 设置界面一键中文化工具)

专为 **Google Antigravity** 客户端打造的设置界面（`Ctrl + ,`）轻量级一键中文化工具。

> 解决官方客户端频繁自动更新后界面重置回英文的痛点，每次更新后只需双击运行一次，即可秒级恢复中文设置界面。

---

## 📌 当前支持版本

- **当前验证版本**：`v2.12.0`（通过动态提取机制，可兼容后续版本更新）
- **支持操作系统**：Windows（支持自定义安装路径）

---

## 🌟 核心特性

- 🎯 **全景深度汉化**：
  - **通用 (General)**：执行策略、排队消息时机、安全预设、制品审核策略、浏览器自动化权限及详细说明。
  - **应用 (Application)**：常驻菜单栏、远程控制 (Remote Control)、通知系统跳转、客户端版本。
  - **外观 (Appearance)**：主题切换（跟随系统/浅色/深色）、主题调色板（预设选择、背景色、前景色、强调色）。
  - **模型与配额 (Models & Usage)**：当前套餐展示、升级套餐入口、AI 额度超额开关、Gemini/Claude/GPT 模型每周及5小时配额卡片与刷新时间动态翻译。
  - **自定义 (Customizations)**：Token 消耗明细、技能（Skills 全局徽标）、规则（Rules）、扩展插件（Plugins）、已安装的 MCP 服务器管理与配置文件入口。
  - **交互元素**：所有下拉选择框（Dropdowns）、按钮（打开、编辑、添加、保存、取消、删除、重置等）、鼠标悬停浮窗（Hover Tooltips）及组织策略提示。
- 🛡️ **安全无损与一键还原**：
  - 首次运行会自动在客户端资源目录生成纯净的官方备份：`app.asar.bak`。
  - 采用官方原生的 `--web_bundle_path` 外挂界面技术，**完全不修改任何核心业务逻辑、网络通信与模型交互**。
  - 提供一键还原脚本，随时随地一键无损回到官方英文原版。
- 🔄 **更新无缝衔接**：
  - 当 Antigravity 自动更新覆盖导致界面回到英文时，只需重新运行一次 `set-chinese.bat` 即可重新注入中文。
- 📝 **词典开源可配**：
  - 附带 `translations_settings.json` 词典文件，任何人都可根据习惯用记事本随时自定义调整翻译文案。

---

## 🚀 使用指南

### 1. 安装与应用中文

1. **完全退出 Antigravity 客户端**（建议在系统托盘右键退出或确保没有残留进程）。
2. 从 [Releases 页面](https://github.com/imsxx/Antigravity-Chinese/releases) 下载最新压缩包并解压（或克隆本仓库）。
3. 双击运行当前目录下的 **`set-chinese.bat`**。
4. 提示汉化成功后，重新打开 Antigravity，按下快捷键 **`Ctrl + ,`**，即可体验全中文设置界面！

> 注：运行需要电脑已安装 Python 3 环境（一般开发环境均已自带）。

### 2. 客户端更新后如何恢复？

每次官方客户端自动更新后：
- 打开本文件夹，再次双击运行 **`set-chinese.bat`**，数秒内即可重新生成并挂载中文界面。

### 3. 如何还原为官方原版英文？

- 双击运行本目录下的 **`restore-english.bat`**，脚本将自动从官方备份恢复 `app.asar` 并移除外挂资源包，立即恢复纯正官方英文。

---

## 📁 目录文件介绍

```text
Antigravity-Chinese/
├── patch_settings.py          # 核心提取、汉化注入与打包引擎
├── translations_settings.json # 设置界面翻译词典（可自由调整）
├── set-chinese.bat            # Windows 一键应用中文脚本
├── restore-english.bat        # Windows 一键还原英文脚本
└── README.md                  # 说明文档
```

---

## 💡 手动指定安装目录（高级）

如果你的 Antigravity 没有安装在默认的 `%LOCALAPPDATA%\Programs\antigravity`，可以通过命令行参数指定：

```bash
# 应用中文补丁
python patch_settings.py --path "D:\Your\Path\To\antigravity"

# 还原为官方英文
python patch_settings.py --restore --path "D:\Your\Path\To\antigravity"
```

---

## ⚖️ 免责声明

本工具仅用于个人本地界面汉化学习与体验优化，不包含任何破解、绕过验证或篡改业务逻辑的代码。项目完全开源透明，不对使用本工具产生的其他衍生问题承担责任。
