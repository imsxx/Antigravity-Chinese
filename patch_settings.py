# -*- coding: utf-8 -*-
"""
Antigravity 设置界面(Ctrl+,)一键中文化工具。

引擎特性:
- 声明式规则:全部翻译规则存放于 translations_settings.json,适配新版本无需改动本文件;
- 匹配类型(按安全性从高到低):keys(键值对,如 label:"X") / quoted(带引号字符串) /
  exact(完整片段) / regex(正则)。所有规则都必须完整匹配,不做裸词全局替换,避免误伤代码;
- 应用自检:统计规则命中率,低于阈值时提示客户端可能已更新,建议运行 --scan 适配;
- --scan 兼容性扫描(列出失效规则与未汉化新增文案) / --verify 干跑验证(不写入客户端);
- --restore 优先逆向移除注入,不依赖备份;备份附带版本标记,避免过期备份覆盖新版本。
"""

import os
import sys
import json
import struct
import io
import shutil
import zipfile
import subprocess
import re
import time
import ctypes

# 命中率低于该阈值时提示客户端可能已更新
HIT_RATE_WARN = 0.8

BAK_MARKER_SUFFIX = '.version'
LS_SCRIPT_PATH = 'dist/languageServer.js'

# 注入锚点与注入文本(逆向还原依赖此文本的逐字匹配,请勿修改)
INSERT_AFTER = "'--enable_sidecars',\n        ];"
INJECT_TEXT = (
    "\n        const _customBundle = path_1.default.join(process.resourcesPath, 'web_bundle.zip');"
    "\n        if (fs.existsSync(_customBundle)) {"
    "\n            args.push(`--web_bundle_path=${_customBundle}`);"
    "\n        }"
)
PATCH_MARKER = '_customBundle'


# ---------------------------------------------------------------- 路径与版本

def get_antigravity_dir(custom_path=None):
    if custom_path and os.path.isdir(custom_path):
        return custom_path
    local_app_data = os.environ.get('LOCALAPPDATA', '')
    default_path = os.path.join(local_app_data, 'Programs', 'antigravity')
    if os.path.isdir(default_path):
        return default_path
    return None


def get_file_version(path):
    """通过 Win32 version.dll 读取文件版本号,失败返回 None。"""
    try:
        size = ctypes.windll.version.GetFileVersionInfoSizeW(str(path), None)
        if not size:
            return None
        buf = ctypes.create_string_buffer(size)
        if not ctypes.windll.version.GetFileVersionInfoW(str(path), 0, size, buf):
            return None
        r = ctypes.c_uint()
        p = ctypes.c_void_p()
        if not ctypes.windll.version.VerQueryValueW(buf, "\\", ctypes.byref(p), ctypes.byref(r)):
            return None

        class VS_FIXEDFILEINFO(ctypes.Structure):
            _fields_ = [
                ('dwSignature', ctypes.c_uint32), ('dwStrucVersion', ctypes.c_uint32),
                ('dwFileVersionMS', ctypes.c_uint32), ('dwFileVersionLS', ctypes.c_uint32),
                ('dwProductVersionMS', ctypes.c_uint32), ('dwProductVersionLS', ctypes.c_uint32),
                ('dwFileFlagsMask', ctypes.c_uint32), ('dwFileFlags', ctypes.c_uint32),
                ('dwFileOS', ctypes.c_uint32), ('dwFileType', ctypes.c_uint32),
                ('dwFileSubtype', ctypes.c_uint32), ('dwFileDateMS', ctypes.c_uint32),
                ('dwFileDateLS', ctypes.c_uint32),
            ]

        info = ctypes.cast(p, ctypes.POINTER(VS_FIXEDFILEINFO)).contents
        return '%d.%d.%d' % (info.dwFileVersionMS >> 16,
                             info.dwFileVersionMS & 0xFFFF,
                             info.dwFileVersionLS >> 16)
    except Exception:
        return None


def is_antigravity_running():
    try:
        out = subprocess.check_output('tasklist /FI "IMAGENAME eq Antigravity.exe"',
                                      shell=True, text=True, errors='ignore')
        return 'Antigravity.exe' in out
    except Exception:
        return False


# ---------------------------------------------------------------- 资源提取

def extract_web_bundle(ls_exe_path):
    """在 language_server.exe 中定位内嵌的界面资源 zip。"""
    print('正在从 language_server.exe 读取内置界面资源...')
    with open(ls_exe_path, 'rb') as f:
        data = f.read()

    idx = 0
    while True:
        idx = data.find(b'PK\x05\x06', idx)
        if idx == -1:
            break
        total_entries, cd_size, cd_offset = struct.unpack('<HII', data[idx + 10:idx + 20])
        zip_start = (idx - cd_size) - cd_offset
        if zip_start >= 0 and data[zip_start:zip_start + 4] == b'PK\x03\x04':
            try:
                candidate = data[zip_start:idx + 22]
                zf = zipfile.ZipFile(io.BytesIO(candidate))
                names = set(zf.namelist())
                if 'main.js' in names and 'index.html' in names:
                    return candidate
            except Exception:
                pass
        idx += 4

    raise RuntimeError('未能在 language_server.exe 中定位到内置界面资源包!')


def unpack_asar(asar_path):
    with open(asar_path, 'rb') as f:
        buf = f.read(16)
        size = struct.unpack('<I', buf[12:16])[0]
        header_json = f.read(size).decode('utf-8')
        header = json.loads(header_json)
        # 数据区起点 = 16 字节 Pickle 头 + JSON 长度按 4 字节对齐后的位置。
        # 官方 asar 的 JSON 长度不一定整除 4(存在 1~3 字节填充),
        # 直接按 size 计算会整体错位,导致所有文件多读填充字节、末尾截断。
        base_offset = 16 + size + ((4 - size % 4) % 4)

        files_data = {}

        def extract_tree(tree, current_path=''):
            for name, info in tree.get('files', {}).items():
                subpath = '%s/%s' % (current_path, name) if current_path else name
                if 'files' in info:
                    extract_tree(info, subpath)
                elif 'link' in info or info.get('unpacked'):
                    pass
                else:
                    offset = int(info['offset'])
                    fsize = int(info['size'])
                    f.seek(base_offset + offset)
                    files_data[subpath] = f.read(fsize)

        extract_tree(header)
        return header, files_data


def pack_asar(header, files_data, out_asar_path):
    file_payloads = []

    def process_tree(node, current_path=''):
        for name, info in node.get('files', {}).items():
            subpath = '%s/%s' % (current_path, name) if current_path else name
            if 'files' in info:
                process_tree(info, subpath)
            elif info.get('unpacked') or 'link' in info:
                pass
            else:
                content = files_data[subpath]
                info['size'] = len(content)
                file_payloads.append((info, content))

    process_tree(header)
    current_offset = 0
    for info, content in file_payloads:
        info['offset'] = str(current_offset)
        current_offset += len(content)

    header_json = json.dumps(header, separators=(',', ':'))
    header_bytes = header_json.encode('utf-8')
    json_len = len(header_bytes)
    pad = (4 - (json_len % 4)) % 4
    header_payload = header_bytes + b'\x00' * pad
    aligned_len = len(header_payload)

    # 遵循 Chromium Pickle 规范:[4, aligned_len + 8, aligned_len + 4, json_len]
    pickle_header = struct.pack('<IIII', 4, aligned_len + 8, aligned_len + 4, json_len)

    with open(out_asar_path, 'wb') as out_f:
        out_f.write(pickle_header)
        out_f.write(header_payload)
        for _, content in file_payloads:
            out_f.write(content)


# ---------------------------------------------------------------- 规则加载与执行

KEY_CATEGORIES = ('label', 'title', 'description', 'displayName', 'sectionTitle',
                  'shortDescription', 'longDescription', 'subtitle',
                  'text', 'tooltipText', 'sidebarLabel', 'searchPlaceholder', 'tooltip')


def load_rules(script_dir):
    path = os.path.join(script_dir, 'translations_settings.json')
    if not os.path.isfile(path):
        print('错误:未找到词典文件 %s' % path)
        return None
    with open(path, 'r', encoding='utf-8') as f:
        data = json.load(f)

    rules = []
    for en, zh in data.get('exact_strings', {}).items():
        if en:
            rules.append({'kind': 'exact', 'name': en, 'zh': zh})
    for en, zh in data.get('quoted_strings', {}).items():
        if '"' in en or '\\' in en:
            print('警告:quoted_strings 条目包含引号或反斜杠,已跳过: %r' % en)
            continue
        rules.append({'kind': 'quoted', 'name': en, 'zh': zh})
    for key, table in data.get('keys', {}).items():
        if key not in KEY_CATEGORIES:
            print('警告:未知的 keys 分类 %r,已跳过。' % key)
            continue
        for en, zh in table.items():
            if '"' in en:
                print('警告:keys.%s 条目包含双引号,已跳过: %r' % (key, en))
                continue
            rules.append({'kind': 'key:%s' % key, 'name': en, 'zh': zh})
    for item in data.get('regex', []):
        rules.append({'kind': 'regex',
                      'name': item.get('note') or item['pattern'][:60],
                      'pattern': item['pattern'], 'zh': item['replace']})
    return rules


# 键值对扫描:以 ":" 为字面量锚点(前缀优化,避免回溯退化,实测 9.6MB 109 秒 -> 0.5 秒)。
# 采用"逐锚点手动推进"而不是整段正则替换:未命中的锚点(如 prefix:["files:"])
# 只跳过 :" 两个字符,绝不回吞后续内容,否则会把下一个真实键值的开引号一起吃掉。
QUOTED_VALUE_RE = re.compile(r'"((?:[^"\\]|\\.)*)"')
WORD_TAIL_RE = re.compile(r'[A-Za-z_$][\w$]*\Z')


def apply_rules(text, rules):
    """应用全部规则。

    性能关键:
    - keys 词典以 ":" 为字面量锚点做单遍扫描,避免正则回溯退化;
    - quoted 词典逐条精确替换(JS 单引号串可内嵌双引号,不可做全局引号配对);
    - 正则规则均要求带字面量前缀,避免逐字符回溯导致分钟级卡顿。
    """
    hit = {}

    def _mark(r, n):
        hit[id(r)] = hit.get(id(r), 0) + n

    # 1) 完整片段替换(长片段优先,避免短片段先替换后破坏长片段的匹配)
    for r in sorted((r for r in rules if r['kind'] == 'exact'), key=lambda r: -len(r['name'])):
        n = text.count(r['name'])
        if n:
            text = text.replace(r['name'], r['zh'])
        _mark(r, n)

    # 2) 正则规则(subn 一次完成替换与计数)
    for r in (r for r in rules if r['kind'] == 'regex'):
        text, n = re.subn(r['pattern'], r['zh'], text)
        _mark(r, n)

    # 3) 词典:键值对与带引号字符串
    kmap = {}
    qmap = {}
    for r in rules:
        if r['kind'].startswith('key:'):
            kmap.setdefault(r['kind'][4:], {})[r['name']] = r
        elif r['kind'] == 'quoted':
            qmap[r['name']] = r

    if kmap:
        src = text
        pieces = []
        pos = 0
        find = src.find
        while True:
            i = find(':"', pos)
            if i < 0:
                pieces.append(src[pos:])
                break
            wm = WORD_TAIL_RE.search(src[max(0, i - 128):i])
            if wm is not None:
                table = kmap.get(wm.group(0))
                if table is not None:
                    vm = QUOTED_VALUE_RE.match(src, i + 1)
                    if vm is not None:
                        r = table.get(vm.group(1).replace('\\"', '"'))
                        if r is not None:
                            _mark(r, 1)
                            pieces.append(src[pos:i + 1])
                            pieces.append('"%s"' % r['zh'])
                            pos = vm.end()
                            continue
            # 未命中:只跳过「:"」两个字符,不回吞后续内容
            pieces.append(src[pos:i + 2])
            pos = i + 2
        text = ''.join(pieces)

    # 4) 带引号字符串:逐条精确替换。
    # 注意:main.js 中单引号字符串可能内嵌双引号(如 description:'...run "X"...'),
    # 不能做全局引号配对扫描,否则引号配对会整体错位导致替换失效。
    for r in sorted((r for r in rules if r['kind'] == 'quoted'), key=lambda r: -len(r['name'])):
        needle = '"%s"' % r['name']
        n = text.count(needle)
        if n:
            text = text.replace(needle, '"%s"' % r['zh'])
        _mark(r, n)

    stats = [(r, hit.get(id(r), 0)) for r in rules]
    return text, stats


def report_stats(stats):
    total = len(stats)
    ok = sum(1 for _, n in stats if n)
    repl = sum(n for _, n in stats)
    print('翻译规则命中: %d/%d 条,合计替换 %d 处' % (ok, total, repl))
    missed = [r for r, n in stats if not n]
    if missed:
        print('未命中规则(%d 条):' % len(missed))
        for r in missed[:15]:
            print('  [%s] %s' % (r['kind'], r['name'][:90]))
        if len(missed) > 15:
            print('  ... 其余 %d 条详见 --scan 报告' % (len(missed) - 15))
    rate = ok / total if total else 0
    if rate < HIT_RATE_WARN:
        print('')
        print('*' * 60)
        print('警告:规则命中率偏低(%.0f%%),客户端可能已更新。' % (rate * 100))
        print('建议运行: python patch_settings.py --scan 检查新增/变更文案。')
        print('*' * 60)
    return ok, total


# ---------------------------------------------------------------- 扫描与验证

def _read_main_js(app_dir):
    resources = os.path.join(app_dir, 'resources')
    ls_exe = os.path.join(resources, 'bin', 'language_server.exe')
    bundle_bytes = extract_web_bundle(ls_exe)
    in_zf = zipfile.ZipFile(io.BytesIO(bundle_bytes))
    return in_zf.read('main.js').decode('utf-8', errors='ignore')


def _is_candidate(v):
    if not (2 <= len(v) <= 200):
        return False
    if not re.search(r'[A-Za-z]{2}', v):
        return False
    if any(x in v for x in ('{', '}', '${', '=>', '://', '\\n', '\\u')):
        return False
    return True


def do_verify(app_dir, rules):
    print('干跑验证(不会写入客户端)...')
    main_js = _read_main_js(app_dir)
    print('main.js 大小: %d 字符' % len(main_js))
    patched, stats = apply_rules(main_js, rules)
    ok, total = report_stats(stats)
    if total and ok == total:
        print('验证通过:全部规则命中。')
    return ok, total


def do_scan(app_dir, rules):
    report = []

    def w(s=''):
        report.append(str(s))

    exe_path = os.path.join(app_dir, 'Antigravity.exe')
    ver = get_file_version(exe_path) or '未知'
    main_js = _read_main_js(app_dir)
    print('客户端版本: %s' % ver)
    print('main.js 大小: %d 字符' % len(main_js))

    w('# Antigravity 汉化兼容性扫描报告')
    w('')
    w('[客户端版本] %s' % ver)
    w('[main.js 大小] %d 字符' % len(main_js))

    patched, stats = apply_rules(main_js, rules)
    ok, total = report_stats(stats)
    w('')
    w('[规则命中] %d/%d 条' % (ok, total))
    w('')
    w('=== 未命中规则(可能已失效,需适配新版本) ===')
    for r, n in stats:
        if not n:
            w('  [%s] %s' % (r['kind'], r['name'][:150]))

    # 设置项注册表
    w('')
    w('=== 设置项注册表(screen / 标识 / label / description) ===')
    seen = set()
    for m in re.finditer(r'\[([A-Za-z]{1,3}\.[A-Z0-9_]+),\{', main_js):
        ident = m.group(1)
        seg = main_js[m.start():m.start() + 1200]
        lm = re.search(r'label:"((?:[^"\\]|\\.)*)"', seg)
        dm = re.search(r"description:(?:'((?:[^'\\]|\\.)*)'|\"((?:[^\"\\]|\\.)*)\"|`([^`]*)`)", seg)
        sm = re.search(r'screen:"([^"]+)"', seg)
        label = lm.group(1) if lm else ''
        desc = (dm.group(1) or dm.group(2) or dm.group(3) or '') if dm else ''
        screen = sm.group(1) if sm else ''
        key = (ident, label)
        if key in seen:
            continue
        seen.add(key)
        w('  [%s] %s' % (screen or '?', ident))
        if label:
            w('      label: %s' % label)
        if desc:
            w('      desc : %s' % desc.replace('\n', ' ')[:160])

    # 未汉化候选
    dict_names = set()
    for r in rules:
        if not r['kind'].startswith('regex'):
            dict_names.add(r['name'])

    w('')
    w('=== 未汉化候选(不在词典中的界面文案) ===')
    mine = [(k, r'%s:"((?:[^"\\]|\\.)*)"' % k) for k in
            ('label', 'title', 'description', 'displayName', 'sectionTitle', 'shortDescription')]
    mine += [(k, r'(?<![A-Za-z0-9_$])%s:"((?:[^"\\]|\\.)*)"' % k) for k in
             ('text', 'tooltipText', 'sidebarLabel', 'searchPlaceholder', 'tooltip')]
    for key, pat in mine:
        counts = {}
        for m in re.finditer(pat, main_js):
            v = m.group(1)
            counts[v] = counts.get(v, 0) + 1
        cand = [(v, c) for v, c in counts.items() if v not in dict_names and _is_candidate(v)]
        cand.sort()
        w('')
        w('--- %s(%d 条)---' % (key, len(cand)))
        for v, c in cand:
            w('  x%d  %s' % (c, v))

    # asar 结构自检
    w('')
    w('=== app.asar 结构自检 ===')
    try:
        resources = os.path.join(app_dir, 'resources')
        header, files = unpack_asar(os.path.join(resources, 'app.asar'))
        ls = files.get(LS_SCRIPT_PATH)
        if ls is None:
            w('  !! 未找到 %s,asar 结构可能已变化' % LS_SCRIPT_PATH)
        else:
            code = ls.decode('utf-8', errors='ignore')
            w('  注入锚点命中: %s' % ('是' if INSERT_AFTER in code else '否'))
            w('  当前是否已打补丁: %s' % ('是' if PATCH_MARKER in code else '否'))
    except Exception as e:
        w('  !! app.asar 解析失败: %s' % e)

    out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'scan_report.txt')
    with open(out_path, 'w', encoding='utf-8') as f:
        f.write('\n'.join(report))
    print('')
    print('扫描完成,完整报告已写入: %s' % out_path)
    return out_path


# ---------------------------------------------------------------- 应用与还原

def _refresh_backup(app_asar, app_asar_bak, bak_marker, cur_ver):
    shutil.copy2(app_asar, app_asar_bak)
    with open(bak_marker, 'w', encoding='utf-8') as f:
        f.write(cur_ver)


def apply_patch(app_dir, rules):
    resources = os.path.join(app_dir, 'resources')
    app_asar = os.path.join(resources, 'app.asar')
    app_asar_bak = os.path.join(resources, 'app.asar.bak')
    bak_marker = app_asar_bak + BAK_MARKER_SUFFIX
    ls_exe = os.path.join(resources, 'bin', 'language_server.exe')
    web_bundle_zip = os.path.join(resources, 'web_bundle.zip')

    if not os.path.isfile(app_asar) or not os.path.isfile(ls_exe):
        print('错误:未能找到 Antigravity 必要核心文件:\n%s\n%s' % (app_asar, ls_exe))
        return False

    cur_ver = get_file_version(os.path.join(app_dir, 'Antigravity.exe')) or '未知'
    print('客户端版本: %s' % cur_ver)

    # 打包自检:官方 asar 原样"解包 -> 重打包"必须与官方文件逐字节一致。
    # 若不一致说明 asar 结构假设有误(如数据区对齐),立即中止,绝不修改客户端。
    print('正在进行 asar 打包自检(解包 -> 重打包 必须与官方逐字节一致)...')
    h_check, f_check = unpack_asar(app_asar)
    tmp_asar = app_asar + '.selftest.tmp'
    pack_asar(h_check, f_check, tmp_asar)
    with open(tmp_asar, 'rb') as tf:
        roundtrip_bytes = tf.read()
    with open(app_asar, 'rb') as af:
        original_bytes = af.read()
    os.remove(tmp_asar)
    if roundtrip_bytes != original_bytes:
        print('错误:asar 打包自检未通过(重打包结果与官方不一致)!')
        print('已中止,客户端未被修改。请运行 --scan 检查结构变化或反馈此问题。')
        return False
    print('打包自检通过。')

    header, files_data = unpack_asar(app_asar)
    if LS_SCRIPT_PATH not in files_data:
        print('错误:app.asar 中未找到 %s' % LS_SCRIPT_PATH)
        return False
    ls_code = files_data[LS_SCRIPT_PATH].decode('utf-8', errors='ignore')
    already_patched = PATCH_MARKER in ls_code

    # 1. 备份官方原版(带版本标记,避免过期备份覆盖新版本)
    if not os.path.isfile(app_asar_bak):
        print('创建官方原版备份: app.asar.bak ...')
        _refresh_backup(app_asar, app_asar_bak, bak_marker, cur_ver)
    else:
        bak_ver = ''
        if os.path.isfile(bak_marker):
            with open(bak_marker, 'r', encoding='utf-8') as f:
                bak_ver = f.read().strip()
        if bak_ver != cur_ver:
            if not already_patched:
                print('检测到客户端已更新(备份版本 %s -> 当前 %s),正在刷新官方备份 ...'
                      % (bak_ver or '未知', cur_ver))
                _refresh_backup(app_asar, app_asar_bak, bak_marker, cur_ver)
            else:
                print('提示:现有备份来自其他版本(%s),可能已过期;还原将优先采用逆向移除,不使用该备份。'
                      % (bak_ver or '未知'))
        else:
            print('已存在官方原版备份: app.asar.bak')

    # 2. 生成中文界面资源包
    bundle_bytes = extract_web_bundle(ls_exe)
    in_zf = zipfile.ZipFile(io.BytesIO(bundle_bytes))

    print('正在汉化 main.js 设置界面文案...', flush=True)
    mjs_content = in_zf.read('main.js').decode('utf-8', errors='ignore')
    t0 = time.time()
    patched_mjs, stats = apply_rules(mjs_content, rules)
    report_stats(stats)
    print('规则应用完成,耗时 %.1f 秒。' % (time.time() - t0))

    print('正在生成本地资源包: web_bundle.zip ...')
    out_buf = io.BytesIO()
    with zipfile.ZipFile(out_buf, 'w', compression=zipfile.ZIP_DEFLATED) as out_zf:
        for item in in_zf.infolist():
            if item.filename == 'main.js':
                out_zf.writestr(item, patched_mjs.encode('utf-8'))
            else:
                out_zf.writestr(item, in_zf.read(item.filename))
    with open(web_bundle_zip, 'wb') as f:
        f.write(out_buf.getvalue())

    # 3. 回读校验资源包
    try:
        with zipfile.ZipFile(web_bundle_zip) as zf:
            names = set(zf.namelist())
            if 'main.js' not in names or zf.getinfo('main.js').file_size == 0:
                print('错误:web_bundle.zip 校验失败!')
                return False
    except Exception as e:
        print('错误:web_bundle.zip 校验失败: %s' % e)
        return False
    print('资源包校验通过。')

    # 4. 配置 app.asar 启用自定义界面资源包
    if not already_patched:
        print('正在配置 app.asar 加载中文界面...')
        if INSERT_AFTER not in ls_code:
            print('错误:未在 app.asar 中找到注入锚点,客户端结构可能已变化!')
            print('建议运行: python patch_settings.py --scan 检查。')
            return False
        ls_code = ls_code.replace(INSERT_AFTER, INSERT_AFTER + INJECT_TEXT, 1)
        files_data[LS_SCRIPT_PATH] = ls_code.encode('utf-8')
        pack_asar(header, files_data, app_asar)

        # 回读校验注入结果
        _, files_check = unpack_asar(app_asar)
        if PATCH_MARKER not in files_check[LS_SCRIPT_PATH].decode('utf-8', errors='ignore'):
            print('错误:app.asar 注入回读校验失败!')
            return False
        print('app.asar 配置成功(回读校验通过)。')
    else:
        print('app.asar 已处于中文化配置状态。')

    print('')
    print('=' * 45)
    print('恭喜!Antigravity 设置界面中文化完成!')
    print('请重启客户端后按 Ctrl+, 查看效果。')
    print('如需还原:重新运行本工具并选择「2. 还原官方英文」(或 --restore)。')
    print('=' * 45)
    print('')
    return True


def restore_backup(app_dir):
    resources = os.path.join(app_dir, 'resources')
    app_asar = os.path.join(resources, 'app.asar')
    app_asar_bak = os.path.join(resources, 'app.asar.bak')
    bak_marker = app_asar_bak + BAK_MARKER_SUFFIX
    web_bundle_zip = os.path.join(resources, 'web_bundle.zip')

    reversed_ok = False
    if os.path.isfile(app_asar):
        try:
            header, files = unpack_asar(app_asar)
            ls = files.get(LS_SCRIPT_PATH)
            if ls is not None:
                code = ls.decode('utf-8', errors='ignore')
                if PATCH_MARKER in code:
                    if INJECT_TEXT in code:
                        print('正在逆向移除界面加载注入(不依赖备份)...')
                        files[LS_SCRIPT_PATH] = code.replace(INJECT_TEXT, '', 1).encode('utf-8')
                        pack_asar(header, files, app_asar)
                        reversed_ok = True
                    else:
                        print('警告:检测到注入标记但文本不匹配,将改用备份还原。')
        except Exception as e:
            print('警告:解析 app.asar 失败(%s),将尝试备份还原。' % e)

    if os.path.isfile(web_bundle_zip):
        os.remove(web_bundle_zip)
        print('已移除 web_bundle.zip。')

    restored = False
    if not reversed_ok and os.path.isfile(app_asar_bak):
        print('正在从官方备份还原 app.asar ...')
        shutil.copy2(app_asar_bak, app_asar)
        restored = True
        cur_ver = get_file_version(os.path.join(app_dir, 'Antigravity.exe')) or '未知'
        if os.path.isfile(bak_marker):
            with open(bak_marker, 'r', encoding='utf-8') as f:
                bak_ver = f.read().strip()
            if bak_ver and cur_ver != '未知' and bak_ver != cur_ver:
                print('警告:备份版本(%s)与当前客户端版本(%s)不一致,请留意。' % (bak_ver, cur_ver))

    if not reversed_ok and not restored:
        print('未发现需要还原的内容(既没有注入标记,也没有官方备份)。')
        return True

    print('')
    print('=' * 45)
    print('已成功还原为官方英文原版!')
    print('重新打开 Antigravity 客户端即可恢复纯英文界面。')
    print('=' * 45)
    print('')
    return True


# ---------------------------------------------------------------- 入口

def main():
    import argparse
    try:
        sys.stdout.reconfigure(encoding='utf-8', line_buffering=True)
    except Exception:
        pass
    parser = argparse.ArgumentParser(description='Antigravity 设置界面一键汉化工具')
    parser.add_argument('--restore', action='store_true', help='还原为官方英文版本')
    parser.add_argument('--scan', action='store_true', help='兼容性扫描:列出失效规则与未汉化新增文案')
    parser.add_argument('--verify', action='store_true', help='干跑验证:只统计规则命中情况,不写入客户端')
    parser.add_argument('--path', type=str, default=None, help='手动指定 Antigravity 安装目录')
    args = parser.parse_args()

    script_dir = os.path.dirname(os.path.abspath(__file__))

    app_dir = get_antigravity_dir(args.path)
    if not app_dir:
        print('未检测到 Antigravity 安装目录,请确认客户端已安装在默认路径或使用 --path 参数指定。')
        sys.exit(1)
    print('定位到 Antigravity 安装目录: %s' % app_dir)

    rules = load_rules(script_dir)
    if rules is None:
        sys.exit(1)

    if args.scan:
        do_scan(app_dir, rules)
        return
    if args.verify:
        do_verify(app_dir, rules)
        return

    if is_antigravity_running():
        print('提示:检测到 Antigravity 正在运行。建议在执行完后重启客户端查看最新效果。\n')

    if args.restore:
        restore_backup(app_dir)
    else:
        apply_patch(app_dir, rules)


if __name__ == '__main__':
    main()
