# AGENTS.md — 维护者 / 智能体手册

> 本文件记录 Antigravity-Chinese 的开发约定与"血泪教训"。客户端每次自动更新后,按本手册适配可避免走弯路。
> **最近一次重大事故的完整复盘见「⚠️ 血泪教训」第 1 条(asar 对齐错误曾导致客户端无法启动)。**

---

## 1. 项目结构

| 文件 | 作用 |
|---|---|
| `main.go` | **主引擎(用户入口)**:路径/版本/进程检测、应用、还原、`--verify`,Win32 集成(version.dll / tasklist / 控制台 CP) |
| `asar.go` | asar 读写(Chromium Pickle)+ **保序 JSON** 解析与紧凑重建 |
| `rules.go` | 词典加载与规则执行(exact / regex / keys / quoted)+ 命中统计 |
| `scan.go` | `--scan` 兼容性扫描报告 |
| `translations_settings.json` | 词典与规则(声明式,适配新版本只改这里,不动代码) |
| `patch_settings.py` | **Python 参考实现(验证基准)**:与 Go 版输出必须逐字节一致 |
| `antigravity-chinese.exe` | **用户入口**:双击显示交互菜单(应用 / 还原 / 扫描 / 验证)并自动暂停;终端调用保持命令行行为(二进制不入库,经 Releases 分发) |
| `scan_report.txt` | `--scan` 生成的报告(已 gitignore) |
| `_work/` | 本地临时分析目录(已 gitignore,**严禁提交任何提取产物**) |
| `_tools/` | 本地 Go 工具链(已 gitignore) |

构建(仅标准库,无需网络):

```bash
go build -trimpath -ldflags "-s -w" -o antigravity-chinese.exe .
```

---

## 2. 日常使用与版本适配流程

**日常(客户端自动更新后界面变回英文):** 双击 `antigravity-chinese.exe` 选 `1. 应用中文补丁`(或终端 `--apply`)。
若输出提示"命中率偏低",说明新版文案有变化,进入适配流程。

**适配新版本:**

1. `antigravity-chinese.exe --scan` → 查看 `scan_report.txt`:
   - 「未命中规则」= 失效条目,需重新定位到新版文案;
   - 「未汉化候选」= 新版新增文案,按需补进词典。
2. 更新 `translations_settings.json`。
3. `antigravity-chinese.exe --verify` → 目标 **全部命中**(不要带着未命中就应用)。
4. **交叉验证**(改了规则/引擎时必做):`python -u _work/hash_py.py`(参考实现打印 sha256)
   与 Go 版 `--verify` 打印的 sha256 必须一致。
5. **模拟安装目录全流程验证**(见第 5 节),通过后才允许动真机。
6. **关闭客户端**(Electron 锁住 asar),再 `antigravity-chinese.exe` 应用。

---

## 3. 词典结构(translations_settings.json)

四类规则,按安全性从高到低,**优先用靠前的**:

| 类型 | 形式 | 说明 |
|---|---|---|
| `exact_strings` | 完整片段 → 中文 | 最稳。适合含 JSX/模板的多段代码 |
| `keys.*` | `label:"X"` → `label:"中"` | 键值对;支持 `label/title/description/subtitle/longDescription/shortDescription/displayName/sectionTitle/text/tooltipText/sidebarLabel/searchPlaceholder/tooltip`,仅匹配 `键:"..."` 完整形式;注意 `"aria-label":"X"`(带引号的键)不会被键值扫描命中,需用 exact |
| `quoted_strings` | `"X"` → `"中"`(含引号) | 全局带引号精确替换;禁止裸词替换;**仅用于确认全部出现处均为展示用途的字符串**(如侧栏 `text:`/`tooltipText:` 复用同一字面量的场景) |
| `regex` | 正则 | 兜底;**必须带字面量前缀**(见教训 2);**不得使用反向引用与环视(RE2 限制,见第 10 节)** |

原则:短词必须带边界(exact / quoted / 键值对);宁多写几条 exact,不写危险正则。

**选型经验(v2.22 侧栏适配)**:
- 侧栏导航项 = `text:"X"` + `tooltipText:"X"`(如 "New Conversation" / "Conversation History")→ 若字面量各处均为展示用途,直接进 `quoted_strings` 一次覆盖全部形式;
- 与 **screen id 同名**的字符串(如 `Customizations`、`Settings` 存在 `screen:"Customizations"` / 路由比较值)→ **禁止 quoted**,只能用 `keys.text` / `keys.tooltipText` / `keys.tooltip` 等属性限定形式;
- `` `See all (${n})` `` 这类模板字符串 → `regex`,模式含字面量前缀并保留 `\1` 变量;
- 平铺在 JSX children 里的句子(如 "Back to Automations")→ `quoted_strings`;
- `sidebarLabel` 是左侧栏面板名(来自 `mS` sidecar 定义:`schedule/extension/automation`)。

---

## 4. ⚠️ 血泪教训(务必遵守)

### 教训 1:asar 数据区起点必须按 4 字节对齐(曾导致客户端无法启动!)

- 官方 `app.asar` 头部格式:`[4, aligned+8, aligned+4, json_len]`(Chromium Pickle),
  其中 `aligned = json_len + (4 - json_len % 4) % 4`。
- **数据区起点 = 16 + aligned(不是 16 + json_len!)**。算错会让每个文件多读出 1~3 字节填充、
  末尾被截断;打包回去客户端直接无法启动,而且"还原"会在坏文件上二次错位、依然启动不了。
- 黄金判据:**官方 asar 原样"解包 → 重打包",输出必须与输入逐字节一致(byte-identical)**。
  引擎已在 `applyPatch` 开头内置该自检,不通过直接中止。
- **索引 JSON 重建必须保序 + 紧凑 + ASCII 转义**(`asar.go` 的 ordered JSON;`json.Marshal` 会打乱键序,
  直接用它必然自检失败)。任何对 `unpackAsar` / `packAsar` 的改动,必须重跑此逐字节测试。

### 教训 2:正则必须带字面量前缀(曾导致 109 秒卡顿)

- 反例:`([A-Za-z_$][\w$]*):"..."` —— 在压缩代码的长标识符上,`[\w$]*` 失败回溯退化为 O(n²),
  9.6MB 文本实测 **109 秒**。
- 正解:以 `:"` 为字面量锚点扫描,再向前回看键名(Go 版 `keyWordBefore` + `quotedValueAt`,实测 **<1 秒**)。
  注意必须**逐锚点手动推进**:未命中的锚点(如 `prefix:["files:"]` 这类诱饵)只能跳过 `:"` 两个字符,
  不能用整段正则替换——否则匹配区间会把下一个真实键值的开引号一并吞掉,造成大批条目漏翻(曾实测 10 条)。
- 性能基准:全量规则(约 1100 条 / 9.6MB)应用耗时应 **≤2 秒**;明显变慢立刻检查回溯问题。

### 教训 3:严禁全局引号配对扫描(曾导致引号类规则全灭)

- `main.js` 中单引号字符串可内嵌双引号(如 `description:'...run "X"...'`),
  全局 `"..."` 配对会整体错位,后续所有引号匹配全部失效。
- `quoted_strings` 必须**逐条精确替换**;引擎已如此实现,勿改回"单遍配对"。

### 教训 4:动 app.asar 前必须关闭客户端

- Electron 运行时会锁住 asar,写入失败可能导致半途状态;进程检测只提示、不强制。

### 教训 5:任何改动先在模拟安装目录验证

- 见第 5 节;**绝不在真机上直接试错**。

---

## 5. 模拟安装目录验证(改引擎 / 词典后必做)

```powershell
New-Item -ItemType Directory -Force -Path _work\fake\resources\bin | Out-Null
Copy-Item "$env:LOCALAPPDATA\Programs\antigravity\resources\app.asar" '_work\fake\resources\app.asar' -Force
Copy-Item "$env:LOCALAPPDATA\Programs\antigravity\resources\bin\language_server.exe" '_work\fake\resources\bin\language_server.exe' -Force

.\antigravity-chinese.exe --path _work/fake              # 打补丁
.\antigravity-chinese.exe --restore --path _work/fake    # 还原
```

校验标准(缺一不可):

- **打补丁后**:`app.asar` 与 Python 参考实现产物 **逐字节一致**;`web_bundle.zip` 除压缩
  字节流外,304 个条目内容与 Python 版零差异;
- **还原后**:`app.asar` 与官方备份 **逐字节一致**(`_work/fake_validate.py` 可直接复用);
- `--verify` 的 sha256 与 `python -u _work/hash_py.py` 输出的基准 sha256 一致;
- `--scan` 报告与 Python 版报告 **逐行相同**(`_work/cmp_scan.py` 可直接复用)。

```python
# 校验思路(参考实现)
import patch_settings as ps
h1, f1 = ps.unpack_asar(官方备份)
h2, f2 = ps.unpack_asar(待校验文件)
diffs = [k for k in f1 if f1[k] != f2.get(k)]          # 期望:仅 languageServer.js(打补丁后)或空(还原后)
ok = f2['dist/languageServer.js'].replace(ps.INJECT_TEXT.encode(), b'', 1) == f1['dist/languageServer.js']
```

---

## 6. 关键锚点(基于 v2.22.0 实测,升级后需复核)

- 注入锚点(`dist/languageServer.js`):`'--enable_sidecars',\n        ];`
- 注入标记:`_customBundle`(还原时逆向移除,结果与官方逐字节一致)
- 注入依赖:`fs`、`path_1` 均为模块顶层常量(TS 编译产物,已验证作用域安全)
- 界面资源:从 `bin/language_server.exe` 内嵌 zip 提取(定位 `PK\x05\x06` 并校验含 `main.js` + `index.html`)
- 设置导航数组:`r7a=[{screen:"General"},...]` 与 `A7a` 映射(导航标签翻译在此注入)
- 设置注册表:`[Qi.XXX,{screen:"General",label:"...",description:"...",settingType:"..."}]`
- 主题卡片:`PRb=[{id:1,displayName:"System",...}]`
- 官方 `--web_bundle_path` 参数:外挂界面资源包的官方机制(注入代码即调用它)

---

## 7. 版本与备份机制

- 版本号:通过 Win32 `version.dll` 读取 `Antigravity.exe` 的 FileVersion(主.次.构建);
- 首次应用创建 `app.asar.bak` + `app.asar.bak.version`(版本标记);
  版本变化且当前未打补丁时自动刷新备份;
- 还原优先"逆向移除注入"(不依赖备份),失败才回退备份;备份版本不一致会告警。

---

## 8. 红线(不可突破)

- 只翻译界面文案;**不碰账号、配额、认证、计费、遥测默认值、任何业务逻辑**;
- **不提交、不打包、不分发任何提取产物**(`web_bundle.zip`、`app.asar`、`main.js` 等);
- 二进制(exe)只经 GitHub Releases 分发,**不入库**(`.gitignore` 已忽略);
- README 与仓库保持"非官方"声明;勿使用官方标识。

---

## 9. 当前状态

- 引擎:**Go 1.21+(仅标准库)**,`antigravity-chinese.exe` 约 2.9MB;Python 版保留为验证基准与回退;
- 已验证版本:**2.22.0**;(2026-10)规则 **1094 条全命中**、1467 处替换;
- 性能:全流程约 **2.1 秒**(读取 158MB exe + 提取 + 应用 + 打包 + 注入 + 回读校验);
- 验收记录:`--verify` sha256 `4d953bf3…c67b` 与 Python 一致;模拟目录 asar 逐字节一致;zip 304 条目零差异;`--scan` 报告 323 行零差异;
- 覆盖范围:**设置界面(Ctrl+,)全部页面** + **侧边栏导航/面板(新建对话、对话历史、自动化/计划任务、UI 插件、自定义、设置、提供反馈)** + **命令面板 / 右键菜单 / 对话区按钮** + **通知与对话框标题、反馈问卷选项、See all/See more 展开器、环境与区域选择等杂项**;
- 扫描候选已覆盖 `label/title/description/displayName/sectionTitle/shortDescription/text/tooltipText/sidebarLabel/searchPlaceholder/tooltip` 十二类属性(新类带词边界防误报);
- 有意保留英文:模型名等来自服务端下发的动态文案、调试/内部面板(轨迹检查器)、LaTeX 渲染选项、内部代号(Hub / CitC / Cog / Google3)、`emptyMessageSingular/Plural`(复数化词形,与外部模板拼接);
- **发布流程**:打 tag 即云端自动构建发布(`.github/workflows/release.yml`)——
  `git tag v2.22.0 && git push origin v2.22.0`,GitHub Actions 会编译 exe、打包(exe + 词典 + README)
  并生成中文 Release 说明(含源码提交链接与压缩包 SHA256);重复推送同一 tag 时自动改为覆盖上传资产;
- 后续(可选):官方新版发布后跑 `--scan`,按报告增量适配即可。

---

## 10. Go 引擎实现约束(改 Go 代码前必读)

1. **RE2 限制**:Go 的 `regexp` 不支持反向引用(`\1` 只能出现在替换串)与环视(`(?=)`/`(?<=)` 等)。
   词典中的 `regex` 规则若需要"同一标识符出现两次"的约束,改写为两个捕获组(见"沙箱模式"与"主题调色板"两条规则);
2. **替换串语法**:词典 JSON 内统一写 Python 风格 `\1`;`convertReplacement` 转为 Go 的 `${1}`,
   并把字面 `$` 转义为 `$$`(替换串含 `${...}` 模板文本,漏转义会被当成变量引用);
3. **保序 JSON**:词典与 asar 索引都必须用 `parseOrderedJSON`(保序),禁止 `map[string]...` 直解;
   asar 打包必须用 `marshalOrderedJSON`(紧凑 + `ensure_ascii` 风格转义),否则破坏逐字节自检;
4. **编码**:启动时 `SetConsoleOutputCP(65001)`;bat 内 `chcp 65001`;
   非 UTF-8 字节按 Python `errors='ignore'` 语义处理(`toValidUTF8`);
5. **字节级操作**:内嵌资源定位用 `bytes.Index` 扫描 `PK\x05\x06` + 小端字段解析,
   再以 `archive/zip` 校验 `main.js` + `index.html`;zip 重写时复制原条目的
   `Method/Modified/ExternalAttrs`,保证内容与顺序不变;
6. **验证是硬门槛**:任何影响输出的改动(引擎逻辑、词典、打包),必须先过第 2 节第 4/5 步的
   sha256 与逐字节交叉验证,否则不许上真机。
