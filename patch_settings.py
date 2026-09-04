# -*- coding: utf-8 -*-
"""
Antigravity Ctrl+, 设置界面一键中文化工具
包含：菜单栏、选项标题、详细描述、下拉选项、悬停浮窗说明、权限二级子页面与操作按钮汉化。
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

def get_antigravity_dir(custom_path=None):
    if custom_path and os.path.isdir(custom_path):
        return custom_path
    local_app_data = os.environ.get('LOCALAPPDATA', '')
    default_path = os.path.join(local_app_data, 'Programs', 'antigravity')
    if os.path.isdir(default_path):
        return default_path
    return None

def is_antigravity_running():
    try:
        out = subprocess.check_output('tasklist /FI "IMAGENAME eq Antigravity.exe"', shell=True, text=True, errors='ignore')
        return 'Antigravity.exe' in out
    except Exception:
        return False

def extract_web_bundle(ls_exe_path):
    print("正在从 language_server.exe 读取内置界面资源...")
    with open(ls_exe_path, "rb") as f:
        data = f.read()

    idx = 0
    found_zip_bytes = None
    while True:
        idx = data.find(b'PK\x05\x06', idx)
        if idx == -1:
            break
        total_entries, cd_size, cd_offset = struct.unpack('<HII', data[idx+10:idx+20])
        zip_start = (idx - cd_size) - cd_offset
        if zip_start >= 0 and data[zip_start:zip_start+4] == b'PK\x03\x04':
            try:
                candidate = data[zip_start:idx+22]
                zf = zipfile.ZipFile(io.BytesIO(candidate))
                names = set(zf.namelist())
                if 'main.js' in names and 'index.html' in names:
                    found_zip_bytes = candidate
                    break
            except Exception:
                pass
        idx += 4

    if not found_zip_bytes:
        raise RuntimeError("未能在 language_server.exe 中定位到内置界面资源包！")
    return found_zip_bytes

def unpack_asar(asar_path):
    with open(asar_path, 'rb') as f:
        buf = f.read(16)
        size = struct.unpack('<I', buf[12:16])[0]
        header_json = f.read(size).decode('utf-8')
        header = json.loads(header_json)
        base_offset = 16 + size

        files_data = {}
        def extract_tree(tree, current_path=""):
            for name, info in tree.get('files', {}).items():
                subpath = f"{current_path}/{name}" if current_path else name
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
    def process_tree(node, current_path=""):
        for name, info in node.get('files', {}).items():
            subpath = f"{current_path}/{name}" if current_path else name
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
    
    # 遵循 Chromium Pickle 规范：[4, aligned_len + 8, aligned_len + 4, json_len]
    pickle_header = struct.pack('<IIII', 4, aligned_len + 8, aligned_len + 4, json_len)

    with open(out_asar_path, 'wb') as out_f:
        out_f.write(pickle_header)
        out_f.write(header_payload)
        for _, content in file_payloads:
            out_f.write(content)

def patch_main_js(mjs_text, trans_dict):
    # 1. 汉化左侧设置导航栏项目名及页面显示标签
    orig_txa = 'tXa=[{screen:"General"},{screen:"App",label:"Application"},{screen:"Appearance"},{screen:"Notifications"},{screen:"Models"},{screen:"Customizations"},{screen:"Browser"},{screen:"Developer"},{screen:"Tab"},{screen:"Editor"},{screen:"CitC Settings"},{screen:"Best of N"},{screen:"Jetski Chat"},{screen:"Labs"},{screen:"Regroup Google3 Chats"}]'
    repl_txa = 'tXa=[{screen:"General",label:"通用"},{screen:"App",label:"应用"},{screen:"Appearance",label:"外观"},{screen:"Notifications",label:"通知"},{screen:"Models",label:"模型"},{screen:"Customizations",label:"自定义"},{screen:"Browser",label:"浏览器"},{screen:"Developer",label:"开发者"},{screen:"Tab",label:"Tab 补全"},{screen:"Editor",label:"编辑器"},{screen:"CitC Settings",label:"CitC 设置"},{screen:"Best of N",label:"Best of N"},{screen:"Jetski Chat",label:"Jetski 对话"},{screen:"Labs",label:"实验室"},{screen:"Regroup Google3 Chats",label:"重组对话"}]'
    if orig_txa in mjs_text:
        mjs_text = mjs_text.replace(orig_txa, repl_txa)

    # 2. 汉化导航栏标题（Settings, Projects, Not in Project, Workspaces）
    mjs_text = mjs_text.replace('z.createElement(Myb,{text:"Settings"})', 'z.createElement(Myb,{text:"设置"})')
    mjs_text = mjs_text.replace('z.createElement(Myb,{text:"Workspaces"})', 'z.createElement(Myb,{text:"工作区"})')
    mjs_text = mjs_text.replace('z.createElement(Myb,{text:"Projects"})', 'z.createElement(Myb,{text:"项目"})')
    mjs_text = mjs_text.replace('select-none"},"Settings")', 'select-none"},"设置")')
    mjs_text = mjs_text.replace('select-none"},"Projects")', 'select-none"},"项目")')
    mjs_text = mjs_text.replace('select-none"},"Not in Project")', 'select-none"},"非项目")')
    mjs_text = mjs_text.replace('select-none"},"Workspaces")', 'select-none"},"工作区")')
    mjs_text = mjs_text.replace('["Not in Project","Conversations"]', '["非项目","对话记录"]')

    # 3. 汉化底部条目（Shortcuts, Provide Feedback, Conversations, Show all）
    mjs_text = mjs_text.replace('z.createElement(t1,{text:"Shortcuts",', 'z.createElement(t1,{text:"Shortcuts",label:"快捷键指南",')
    mjs_text = mjs_text.replace('z.createElement(t1,{text:"Provide Feedback",', 'z.createElement(t1,{text:"Provide Feedback",label:"提供反馈",')
    mjs_text = mjs_text.replace('z.createElement(t1,{text:"Conversations",', 'z.createElement(t1,{text:"Conversations",label:"对话记录",')
    mjs_text = mjs_text.replace('font-medium outline-none",onClick:()=>{w(!0)}},"Show all")', 'font-medium outline-none",onClick:()=>{w(!0)}},"显示全部")')

    # 4. 汉化安全预设 (Security Preset) 的下拉选项名称及对应悬停说明
    mjs_text = mjs_text.replace('displayName:"Default"', 'displayName:"默认安全模式"')
    mjs_text = mjs_text.replace('displayName:"Full machine"', 'displayName:"整机访问模式"')
    mjs_text = mjs_text.replace('displayName:"Turbo mode"', 'displayName:"Turbo 极速模式"')
    mjs_text = mjs_text.replace('shortDisplayName:"Default"', 'shortDisplayName:"默认"')
    mjs_text = mjs_text.replace('shortDisplayName:"Machine"', 'shortDisplayName:"整机"')
    mjs_text = mjs_text.replace('shortDisplayName:"Turbo"', 'shortDisplayName:"Turbo"')

    # 下拉选项的实时 resolver 汉化
    mjs_text = mjs_text.replace(
        'yb==="use_global"?"Inherit General":yb==="custom"?"Custom":yt[Ma][Da]?.displayName??"Custom"',
        'yb==="use_global"?"继承通用设置":yb==="custom"?"自定义":yt[Ma][Da]?.displayName??"自定义"'
    )
    mjs_text = mjs_text.replace(
        'yb==="use_global"?"Inherit General":yb==="custom"?"Custom":yt[Ma][yb]?.displayName??"Custom"',
        'yb==="use_global"?"继承通用设置":yb==="custom"?"自定义":yt[Ma][yb]?.displayName??"自定义"'
    )
    mjs_text = mjs_text.replace(
        'yb==="use_global"?"Inherits your General settings when working in this project.":yb==="custom"?"Manually customize individual settings.":yt[Ma][yb]?.shortDescription',
        'yb==="use_global"?"在此项目中工作时继承通用设置。":yb==="custom"?"手动自定义各项单独设置。":yt[Ma][yb]?.shortDescription'
    )

    # 5. 汉化制品审核策略 (Artifact Review Policy) 下拉选项与当前值
    mjs_text = re.sub(
        r'value:a==="use_global"\?"Inherit General":a===\s*"turbo"\?"Always Proceed":"Always Ask"',
        'value:a==="use_global"?"继承通用设置":a==="turbo"?"始终直接继续":"始终询问审核"',
        mjs_text
    )
    mjs_text = re.sub(
        r'displayResolver:yb=>yb==="use_global"\?"Inherit General":yb===\s*"turbo"\?"Always Proceed":"Always Ask"',
        'displayResolver:yb=>yb==="use_global"?"继承通用设置":yb==="turbo"?"始终直接继续":"始终询问审核"',
        mjs_text
    )
    mjs_text = mjs_text.replace(
        'UI=[{value:H.ArtifactReviewMode.TURBO,label:"Always Proceed",description:"Agent never asks for review. This maximizes the autonomy of the Agent, but also has the highest risk of the Agent operating over unsafe or injected Artifact content.",disabledInSecureMode:!0},{value:H.ArtifactReviewMode.ALWAYS,label:"Always Ask",description:"Agent always asks for review.",disabledInSecureMode:!1}];',
        'UI=[{value:H.ArtifactReviewMode.TURBO,label:"始终直接继续",description:"智能体从不请求审核。这最大化了智能体的自主性，但也有更高的操作风险。",disabledInSecureMode:!0},{value:H.ArtifactReviewMode.ALWAYS,label:"始终询问审核",description:"智能体在生成或修改文档时始终向你请求审核。",disabledInSecureMode:!1}];'
    )

    # 6. 汉化终端命令自动执行 (Commands Auto Execution) 下拉选项
    mjs_text = mjs_text.replace(
        'yb===H.CascadeCommandsAutoExecution.UNSPECIFIED?"Inherit General":yb===H.CascadeCommandsAutoExecution.EAGER?"Always Proceed":yb===H.CascadeCommandsAutoExecution.PROCEED_IN_SANDBOX?"Proceed in Sandbox":"Require Review"',
        'yb===H.CascadeCommandsAutoExecution.UNSPECIFIED?"继承通用设置":yb===H.CascadeCommandsAutoExecution.EAGER?"始终直接继续":yb===H.CascadeCommandsAutoExecution.PROCEED_IN_SANDBOX?"在沙箱中执行":"需要审核"'
    )
    mjs_text = mjs_text.replace(
        'case H.CascadeCommandsAutoExecution.EAGER:return"Always Proceed";case H.CascadeCommandsAutoExecution.OFF:return"Request Review";default:return"Proceed in Sandbox"',
        'case H.CascadeCommandsAutoExecution.EAGER:return"始终直接继续";case H.CascadeCommandsAutoExecution.OFF:return"请求审核";default:return"在沙箱中执行"'
    )

    # 7. 汉化工作区外文件访问策略 (Outside of folders file access policy) 下拉选项
    mjs_text = mjs_text.replace(
        'yb===H.AgentSettingPolicy.UNSPECIFIED?"Inherit General":yb===H.AgentSettingPolicy.ALLOW?"Allow":yb===H.AgentSettingPolicy.DENY?"Deny":"Always Ask"',
        'yb===H.AgentSettingPolicy.UNSPECIFIED?"继承通用设置":yb===H.AgentSettingPolicy.ALLOW?"允许":yb===H.AgentSettingPolicy.DENY?"拒绝":"始终询问"'
    )

    # 8. 汉化浏览器 JS 执行策略 (Browser JS Execution Policy) 下拉选项
    mjs_text = mjs_text.replace(
        'case H.BrowserJsExecutionPolicy.DISABLED:return"Disabled";case H.BrowserJsExecutionPolicy.ALWAYS_ASK:return"Request Review";case H.BrowserJsExecutionPolicy.TURBO:return"Always Proceed";',
        'case H.BrowserJsExecutionPolicy.DISABLED:return"已禁用";case H.BrowserJsExecutionPolicy.ALWAYS_ASK:return"请求审核";case H.BrowserJsExecutionPolicy.TURBO:return"始终直接继续";'
    )

    # 9. 汉化排队消息策略 (Queued Messages) 选项与悬停浮窗
    mjs_text = mjs_text.replace('case R.MessageDeliveryStrategy.NEXT_INVOCATION:return"Send Immediately";default:return"Queue"', 'case R.MessageDeliveryStrategy.NEXT_INVOCATION:return"立即发送";default:return"排队"')
    mjs_text = mjs_text.replace('case R.MessageDeliveryStrategy.NEXT_INVOCATION:return"Interrupt the agent and send immediately.";default:return"Queue until after the agent completes the turn."', 'case R.MessageDeliveryStrategy.NEXT_INVOCATION:return"打断智能体并立即发送。";default:return"排队等待智能体完成当前轮次后再发送。"')
    mjs_text = mjs_text.replace('`${e} Sends immediately`:`${e} Queues after the turn`', '`${e} 立即发送`:`${e} 本轮结束后排队`')
    mjs_text = mjs_text.replace('`${a} Queues after the turn`:`${a} Sends immediately`', '`${a} 本轮结束后排队`:`${a} 立即发送`')
    mjs_text = mjs_text.replace('`${a} On empty prompt, sends next in queue`', '`${a} 输入框为空时，发送队列中的下一条`')

    # 10. 汉化对话框宽度 (Conversation Width) 与 补全速度 (Autocomplete Speed)
    mjs_text = mjs_text.replace('switch(b){case 2:return"Narrow";case 3:return"Wide";default:return"Default"}', 'switch(b){case 2:return"较窄";case 3:return"较宽";default:return"默认"}')
    mjs_text = mjs_text.replace('switch(b){case H.AutocompleteSpeed.FAST:return"Fast";case H.AutocompleteSpeed.SLOW:return"Slow";default:return"Fast"}', 'switch(b){case H.AutocompleteSpeed.FAST:return"快速";case H.AutocompleteSpeed.SLOW:return"慢速";default:return"快速"}')

    # 11. 汉化权限列表中的模式下拉选项 (allow, ask, deny)
    mjs_text = mjs_text.replace('options:["allow","ask","deny"],displayResolver:n=>n', 'options:["allow","ask","deny"],displayResolver:n=>n==="allow"?"允许":n==="ask"?"询问":"拒绝"')
    mjs_text = mjs_text.replace('options:["allow","ask","deny"],displayResolver:B=>B', 'options:["allow","ask","deny"],displayResolver:B=>B==="allow"?"允许":B==="ask"?"询问":"拒绝"')

    # 12. 汉化外观主题卡片 (System, Light, Dark) 与主题设置
    mjs_text = mjs_text.replace(
        'Cvb=[{id:1,displayName:"System",previewImageUrl:"/system.png"},{id:2,displayName:"Light",previewImageUrl:"/light.png"},{id:3,displayName:"Dark",previewImageUrl:"/dark.png"}]',
        'Cvb=[{id:1,displayName:"跟随系统",previewImageUrl:"/system.png"},{id:2,displayName:"浅色",previewImageUrl:"/light.png"},{id:3,displayName:"深色",previewImageUrl:"/dark.png"}]'
    )
    mjs_text = mjs_text.replace('default:E=a?"Inherit Editor":"System"', 'default:E=a?"继承编辑器":"跟随系统"')
    mjs_text = mjs_text.replace('case 2:E="Light";', 'case 2:E="浅色";')
    mjs_text = mjs_text.replace('case 3:E="Dark";', 'case 3:E="深色";')
    mjs_text = mjs_text.replace(
        'options:Object.keys(G$[e]),displayResolver:N=>N',
        'options:Object.keys(G$[e]),displayResolver:N=>N==="Default Light"?"默认浅色":N==="Default Dark"?"默认深色":N'
    )

    # 13. 汉化设置项内的各个操作按钮
    mjs_text = mjs_text.replace('>{za("tools")}},"Open")', '>{za("tools")}},"打开")')
    mjs_text = mjs_text.replace('>{za("file")}},"Open")', '>{za("file")}},"打开")')
    mjs_text = mjs_text.replace('>{za("network")}},"Open")', '>{za("network")}},"打开")')
    mjs_text = mjs_text.replace('>{za("command")}},"Open")', '>{za("command")}},"打开")')
    mjs_text = mjs_text.replace('>{za("unsandboxed")}},"Open")', '>{za("unsandboxed")}},"打开")')
    mjs_text = mjs_text.replace('>{za("mcp")}},"Open")', '>{za("mcp")}},"打开")')
    mjs_text = mjs_text.replace('>{za("github")}},"Open")', '>{za("github")}},"打开")')
    mjs_text = mjs_text.replace('onClick:()=>K(!0)},"Edit")', 'onClick:()=>K(!0)},"编辑")')
    mjs_text = mjs_text.replace('},"Add MCP",', '},"添加 MCP",')
    mjs_text = mjs_text.replace('"Open MCP Config"', '"打开 MCP 配置文件"')
    mjs_text = mjs_text.replace('},"Customize")', '},"自定义")')

    # 14. 汉化页面标题与各页面顶部文本
    # General
    mjs_text = mjs_text.replace('title:"General",description:', 'title:"通用",description:')
    mjs_text = mjs_text.replace('z.createElement("span",{className:"font-semibold"},"Keyboard shortcuts")', 'z.createElement("span",{className:"font-semibold"},"快捷键")')
    mjs_text = mjs_text.replace('"Configure the browser subagent. It requires"," "', '"配置浏览器子智能体。此功能需要安装 "," "')
    mjs_text = mjs_text.replace('"to be installed."', '""')
    mjs_text = mjs_text.replace('"The browser subagent can be invoked by typing /browser in the conversation input box."', '"在对话输入框中键入 /browser 即可调用浏览器子智能体。"')
    
    # Application
    mjs_text = mjs_text.replace('title:"Application",description:`Manage ${b} app settings.`', 'title:"应用",description:`管理 ${b} 客户端应用设置。`')
    mjs_text = mjs_text.replace('sectionTitle:"General"', 'sectionTitle:"常规"')
    mjs_text = mjs_text.replace('title:a="Notifications"', 'title:a="通知"')
    mjs_text = mjs_text.replace('z.createElement("div",{className:"text-sm font-medium"},"Version")', 'z.createElement("div",{className:"text-sm font-medium"},"版本")')

    # Appearance
    mjs_text = mjs_text.replace('title:"Appearance",description:"Configure the agent\'s visual theme and display preferences."', 'title:"外观",description:"配置智能体的视觉主题与显示偏好。"')

    # Models & Usage
    mjs_text = mjs_text.replace('z.createElement("span",null,"Models & Usage")', 'z.createElement("span",null,"模型与配额")')
    mjs_text = mjs_text.replace('label:z.createElement("div",{className:"text-sm font-medium"},"Your Plan: ",n)', 'label:z.createElement("div",{className:"text-sm font-medium"},"当前套餐：",n)')
    mjs_text = mjs_text.replace('p=c?.userTier?.upgradeSubscriptionText||""', 'p=(c?.userTier?.upgradeSubscriptionText||"").replace("You can upgrade to a Google AI Ultra plan to receive higher rate limits.","你可以升级至 Google AI Ultra 套餐以获取更高的速率限制。")')
    mjs_text = mjs_text.replace('c.userTier.upgradeButtonText||"Upgrade"', '"升级套餐"')
    mjs_text = mjs_text.replace(
        'z.createElement("span",null,a.displayName)',
        'z.createElement("span",null,a.displayName==="Gemini Models"?"Gemini 模型":a.displayName==="Claude and GPT models"?"Claude 与 GPT 模型":a.displayName)'
    )
    mjs_text = mjs_text.replace(
        'label:z.createElement("span",{className:e?"text-secondary-foreground":""},a)',
        'label:z.createElement("span",{className:e?"text-secondary-foreground":""},a==="Weekly Limit Remaining"?"每周剩余配额":a==="Five Hour Limit Remaining"?"5小时剩余配额":a)'
    )
    mjs_text = mjs_text.replace(
        'description:c,rightElement:z.createElement("div"',
        'description:typeof c==="string"?c.replace("You have used some of your weekly limit, it will fully refresh in ","你已消耗部分每周配额，将在 ").replace("You have used some of your 5-hour limit, it will fully refresh in ","你已消耗部分5小时配额，将在 ").replace(" days, "," 天 ").replace(" hours."," 小时后完全刷新。").replace(" hours, "," 小时 ").replace(" minutes."," 分钟后完全刷新。"):c,rightElement:z.createElement("div"'
    )

    # Customizations
    mjs_text = mjs_text.replace('title:"Customizations",description:', 'title:"自定义",description:')
    mjs_text = mjs_text.replace('title:"Skills",count:A.length,collapsible:!0', 'title:"技能",count:A.length,collapsible:!0')
    mjs_text = mjs_text.replace('title:"Rules",count:B.length,collapsible:!0', 'title:"规则",count:B.length,collapsible:!0')
    mjs_text = mjs_text.replace('title:"Plugins",count:D.length,collapsible:!0', 'title:"插件",count:D.length,collapsible:!0')
    mjs_text = mjs_text.replace('title:"Custom Agents",count:C.length,collapsible:!0', 'title:"自定义智能体",count:C.length,collapsible:!0')
    mjs_text = mjs_text.replace(
        'a?"Global":c?b?b.length>15?b.substring(0,15)+"\\u2026":b:"Workspace":"Workspace"',
        'a?"全局":c?b?b.length>15?b.substring(0,15)+"\\u2026":b:"工作区":"工作区"'
    )

    # 15. 汉化严格模式与组织策略悬停浮窗 (Jk 函数)
    mjs_text = mjs_text.replace(
        'case 2:return"This option is disabled while strict mode is enabled.";case 1:return"This option is disabled by your organization."',
        'case 2:return"启用严格模式时此选项已被禁用。";case 1:return"此选项已被你的组织禁用。"'
    )
    mjs_text = mjs_text.replace(
        'case 2:return"This setting cannot be changed while strict mode is enabled.";case 1:return"This setting is managed by your organization."',
        'case 2:return"启用严格模式时无法更改此设置。";case 1:return"此设置由你的组织统一管理。"'
    )

    # 16. 汉化权限预设 (m1 对象)
    mjs_text = mjs_text.replace('label:"Request Review",description:"Every terminal command requires approval."', 'label:"请求审核",description:"所有终端命令均需要批准。"')
    mjs_text = mjs_text.replace('label:"Default",description:"Ask for permission for sensitive operations."', 'label:"默认",description:"对敏感操作请求许可。"')
    mjs_text = mjs_text.replace('label:"Vetted (beta)",description:"A Gemini-powered security agent decides if commands should be auto-approved."', 'label:"审核评估 (Beta)",description:"由 Gemini 驱动的安全代理判断命令是否自动批准。"')

    # 17. 汉化信息浮窗标题与引导
    mjs_text = mjs_text.replace('z.createElement("span",null,"Learn more about"," "', 'z.createElement("span",null,"了解更多关于 "," "')

    # 18. 汉化来自 translations_settings.json 的字典项
    exact_strings = trans_dict.get("exact_strings", {})
    for en, zh in exact_strings.items():
        mjs_text = mjs_text.replace(en, zh)

    labels = trans_dict.get("labels", {})
    for en, zh in labels.items():
        mjs_text = mjs_text.replace(f'label:"{en}"', f'label:"{zh}"')

    titles = trans_dict.get("titles", {})
    for en, zh in titles.items():
        mjs_text = mjs_text.replace(f'title:"{en}"', f'title:"{zh}"')

    tooltips = trans_dict.get("tooltips", {})
    for en, zh in tooltips.items():
        mjs_text = mjs_text.replace(f'"{en}"', f'"{zh}"')

    return mjs_text

def apply_patch(app_dir):
    resources_dir = os.path.join(app_dir, "resources")
    app_asar = os.path.join(resources_dir, "app.asar")
    app_asar_bak = os.path.join(resources_dir, "app.asar.bak")
    ls_exe = os.path.join(resources_dir, "bin", "language_server.exe")
    web_bundle_zip = os.path.join(resources_dir, "web_bundle.zip")

    if not os.path.isfile(app_asar) or not os.path.isfile(ls_exe):
        print(f"错误：未能找到 Antigravity 必要核心文件：\n{app_asar}\n{ls_exe}")
        return False

    # 1. 备份原版 app.asar（仅当备份不存在时创建，保留最纯净官方原版）
    if not os.path.isfile(app_asar_bak):
        print("创建官方原版备份: app.asar.bak ...")
        shutil.copy2(app_asar, app_asar_bak)
    else:
        print("已存在官方原版备份: app.asar.bak")

    # 2. 读取翻译词典
    script_dir = os.path.dirname(os.path.abspath(__file__))
    dict_path = os.path.join(script_dir, "translations_settings.json")
    trans_dict = {}
    if os.path.isfile(dict_path):
        with open(dict_path, "r", encoding="utf-8") as f:
            trans_dict = json.load(f)
    else:
        print(f"警告：未找到词典文件 {dict_path}，将使用内置核心词条。")

    # 3. 提取并中文化界面资源包
    bundle_bytes = extract_web_bundle(ls_exe)
    in_zf = zipfile.ZipFile(io.BytesIO(bundle_bytes))
    
    print("正在全面汉化 main.js 设置界面文本、下拉框、悬停浮窗与子菜单...")
    mjs_content = in_zf.read('main.js').decode('utf-8', errors='ignore')
    patched_mjs = patch_main_js(mjs_content, trans_dict)

    # 4. 打包为 web_bundle.zip
    print(f"正在生成本地资源包: {web_bundle_zip} ...")
    out_buf = io.BytesIO()
    with zipfile.ZipFile(out_buf, 'w', compression=zipfile.ZIP_DEFLATED) as out_zf:
        for item in in_zf.infolist():
            if item.filename == 'main.js':
                out_zf.writestr(item, patched_mjs.encode('utf-8'))
            else:
                out_zf.writestr(item, in_zf.read(item.filename))

    with open(web_bundle_zip, 'wb') as f:
        f.write(out_buf.getvalue())

    # 5. 修改 app.asar 启用自定义界面资源包
    print("正在配置 app.asar 加载中文界面...")
    header, files_data = unpack_asar(app_asar)
    ls_script_path = "dist/languageServer.js"
    if ls_script_path not in files_data:
        print(f"错误：app.asar 中未找到 {ls_script_path}")
        return False

    ls_code = files_data[ls_script_path].decode('utf-8', errors='ignore')
    patch_marker = "_customBundle"
    if patch_marker not in ls_code:
        target = "'--enable_sidecars',\n        ];"
        repl = "'--enable_sidecars',\n        ];\n        const _customBundle = path_1.default.join(process.resourcesPath, 'web_bundle.zip');\n        if (fs.existsSync(_customBundle)) {\n            args.push(`--web_bundle_path=${_customBundle}`);\n        }"
        ls_code = ls_code.replace(target, repl)
        files_data[ls_script_path] = ls_code.encode('utf-8')
        pack_asar(header, files_data, app_asar)
        print("app.asar 配置成功！")
    else:
        print("app.asar 已处于中文化配置状态。")

    print("\n=============================================")
    print("恭喜！Antigravity 设置界面已按截图全景深度汉化！")
    print("涵盖了：通用、应用、外观、模型与配额、自定义各页面。")
    print("请重启客户端按 Ctrl+, 查看最新效果！")
    print("=============================================\n")
    return True

def restore_backup(app_dir):
    resources_dir = os.path.join(app_dir, "resources")
    app_asar = os.path.join(resources_dir, "app.asar")
    app_asar_bak = os.path.join(resources_dir, "app.asar.bak")
    web_bundle_zip = os.path.join(resources_dir, "web_bundle.zip")

    if not os.path.isfile(app_asar_bak):
        print("未发现官方备份文件 app.asar.bak，无需还原。")
        return True

    print("正在从官方备份还原 app.asar ...")
    shutil.copy2(app_asar_bak, app_asar)
    if os.path.isfile(web_bundle_zip):
        os.remove(web_bundle_zip)

    print("\n=============================================")
    print("已成功还原为官方英文原版！")
    print("重新打开 Antigravity 客户端即可恢复纯英文界面。")
    print("=============================================\n")
    return True

def main():
    import argparse
    parser = argparse.ArgumentParser(description="Antigravity 设置界面一键汉化工具")
    parser.add_argument("--restore", action="store_true", help="还原为官方英文版本")
    parser.add_argument("--path", type=str, default=None, help="手动指定 Antigravity 安装目录")
    args = parser.parse_args()

    app_dir = get_antigravity_dir(args.path)
    if not app_dir:
        print("未检测到 Antigravity 安装目录，请确认客户端已安装在默认路径或使用 --path 参数指定。")
        sys.exit(1)

    print(f"定位到 Antigravity 安装目录: {app_dir}")

    if is_antigravity_running():
        print("提示：检测到 Antigravity 正在运行。建议在执行完后重启客户端查看最新效果。\n")

    if args.restore:
        restore_backup(app_dir)
    else:
        apply_patch(app_dir)

if __name__ == "__main__":
    main()
