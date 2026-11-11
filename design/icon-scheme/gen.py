#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""生成「图标替换方案」对比页：拉取多套图标，按真实场景渲染。"""
import json, os, re, time, urllib.request, urllib.error, html

HERE = os.path.dirname(os.path.abspath(__file__))
CACHE = os.path.join(HERE, "cache.json")
BASE = "https://api.iconify.design"

SLOTS = [
    ("task", "备份任务"),
    ("repo", "快照仓库"),
    ("snap", "快照"),
    ("hist", "历史"),
    ("restore", "还原"),
    ("local", "本地设备"),
    ("cloud", "云端"),
    ("lock", "加密"),
]

SCHEMES = [
    {
        "key": "A", "name": "现状 · Tabler 线性 + 浅色底", "prefix": "tabler",
        "tag": "MIT", "note": "目前线上就是这套：彩色浅底方块 + 空心描边图标，状态说了两遍。",
        "box": "tint", "color": "#3370ff",
        "icons": {"task": "folder", "repo": "stack-2", "snap": "camera", "hist": "history",
                  "restore": "arrow-back-up", "local": "device-desktop", "cloud": "cloud", "lock": "lock"},
    },
    {
        "key": "B", "name": "Phosphor Duotone · 双色调", "prefix": "ph",
        "tag": "MIT", "note": "同一图形两层深浅，有体积感又不花哨；去掉色块，状态交给右侧标签。",
        "box": "none", "color": "#3370ff",
        "icons": {"task": "folder-duotone", "repo": "stack-duotone", "snap": "camera-duotone",
                  "hist": "clock-counter-clockwise-duotone", "restore": "arrow-u-up-left-duotone",
                  "local": "desktop-tower-duotone", "cloud": "cloud-duotone", "lock": "lock-duotone"},
    },
    {
        "key": "C", "name": "Phosphor Fill · 实心 + 实色底", "prefix": "ph",
        "tag": "MIT", "note": "色块改成实色、图标反白。最有「原生 App」感，但面积重，只建议用在关键入口。",
        "box": "solid", "color": "#3370ff",
        "icons": {"task": "folder-fill", "repo": "stack-fill", "snap": "camera-fill",
                  "hist": "clock-counter-clockwise-fill", "restore": "arrow-u-up-left-fill",
                  "local": "desktop-tower-fill", "cloud": "cloud-fill", "lock": "lock-fill"},
    },
    {
        "key": "D", "name": "Solar Bold Duotone · 现代双色", "prefix": "solar",
        "tag": "CC BY 4.0", "note": "线条更厚、转角更圆，双色但只用一个色相。当下最主流的 SaaS 观感。",
        "box": "none", "color": "#3370ff",
        "icons": {"task": "folder-bold-duotone", "repo": "layers-bold-duotone", "snap": "gallery-bold-duotone",
                  "hist": "history-bold-duotone", "restore": "restart-bold-duotone",
                  "local": "monitor-bold-duotone", "cloud": "cloud-bold-duotone", "lock": "lock-keyhole-bold-duotone"},
    },
    {
        "key": "E", "name": "Streamline Color · 固定靛蓝双色", "prefix": "streamline-color",
        "tag": "CC BY 4.0", "note": "浅靛蓝填充 + 深靛蓝描边，配色固定、不能跟随状态。而且没有「历史/还原」的原生语义，只能用近似图标凑。",
        "box": "none", "color": "#3370ff",
        "icons": {"task": "new-folder", "repo": "layers-1", "snap": "pictures-folder-memories",
                  "hist": "circle-clock", "restore": "arrow-round-left",
                  "local": "database-server-1", "cloud": "cloud", "lock": "keyhole-lock-circle"},
    },
    {
        "key": "F", "name": "Soft 3D · 柔和立体", "prefix": "withicons-soft3d",
        "tag": "MIT", "note": "有材质和光影，质感最好。代价：图标自带固定色，无法跟随成功/失败变色，列表里连续出现也很重。",
        "box": "none", "color": "#3370ff",
        "icons": {"task": "folder", "repo": "layers", "snap": "clock", "hist": "history",
                  "restore": "undo", "local": "server", "cloud": "cloud", "lock": "lock"},
    },
    {
        "key": "G", "name": "Fluent Emoji Flat · 扁平拟物", "prefix": "fluent-emoji-flat",
        "tag": "MIT", "note": "最活泼，但缺「历史/还原」这类系统语义，且自带颜色与状态色直接冲突，只能做空状态插图。",
        "box": "none", "color": "#3370ff",
        "icons": {"task": "file-folder", "repo": "card-file-box", "snap": "alarm-clock",
                  "hist": "mantelpiece-clock", "restore": "open-file-folder",
                  "local": "desktop-computer", "cloud": "cloud", "lock": "locked"},
    },
]


def load_cache():
    if os.path.exists(CACHE):
        return json.load(open(CACHE, encoding="utf-8"))
    return {}


def fetch(prefix, names):
    """返回 (bodies, (w, h))。不同图标集的 viewBox 不同，必须按集取。"""
    cache = load_cache()
    wh = cache.setdefault("__wh", {})
    got = cache.setdefault(prefix, {})
    miss = [n for n in names if n not in got]
    if miss or prefix not in wh:
        for attempt in range(4):
            try:
                url = f"{BASE}/{prefix}.json?icons={','.join(miss) or 'folder'}"
                req = urllib.request.Request(url, headers={"User-Agent": "icon-scheme/1.0"})
                with urllib.request.urlopen(req, timeout=30) as r:
                    d = json.loads(r.read().decode())
                wh[prefix] = [d.get("width") or 24, d.get("height") or 24]
                for n, v in (d.get("icons") or {}).items():
                    got[n] = v["body"]
                for n in miss:
                    got.setdefault(n, "")
                json.dump(cache, open(CACHE, "w", encoding="utf-8"), ensure_ascii=False)
                break
            except urllib.error.HTTPError as e:
                if e.code == 429:
                    time.sleep(5 + attempt * 5)
                    continue
                raise
            except Exception:
                time.sleep(3)
    return got, tuple(wh.get(prefix) or (24, 24))


_uid = [0]


def svg(prefix, name, size, color=None, extra_cls=""):
    bodies, (vw, vh) = fetch(prefix, [name])
    body = bodies.get(name) or ""
    if not body:
        return f'<span class="miss" style="width:{size}px;height:{size}px">{size}</span>'
    _uid[0] += 1
    u = _uid[0]
    # 给 id / url(#) 加实例后缀，避免同页重复 id 冲突
    body = re.sub(r'id="([^"]+)"', lambda m: f'id="{m.group(1)}-{u}"', body)
    body = re.sub(r'url\(#([^)]+)\)', lambda m: f'url(#{m.group(1)}-{u})', body)
    style = f' style="color:{color}"' if color else ""
    return (f'<svg class="{extra_cls}" width="{size}" height="{size}" viewBox="0 0 {vw} {vh}" '
            f'fill="none" aria-hidden="true"{style}>{body}</svg>')


ROWS = [
    {"kind": "task", "title": "相册资料", "sub": "/vol1/1000/Photos · 12.4 GB", "state": "ok"},
    {"kind": "repo", "title": "天翼云 · 上海", "sub": "12 个快照 · 最近 2 小时前", "state": "ok"},
    {"kind": "hist", "title": "相册资料", "sub": "备份 · 03:12 · 1.2 GB", "state": "bad"},
    {"kind": "restore", "title": "相册资料", "sub": "还原 · 03:20 · 进行中", "state": "run"},
]

STATE_COLOR = {"ok": "#12a150", "bad": "#e5484d", "run": "#3370ff"}
TINT = {"ok": "#e8f8ee", "bad": "#fdecec", "run": "#eaf1ff"}
TINT_FG = {"ok": "#12a150", "bad": "#e5484d", "run": "#3370ff"}


def scheme_block(s):
    pre, icons, box = s["prefix"], s["icons"], s["box"]
    rows = []
    for r in ROWS:
        ic = icons["task"] if r["kind"] == "task" else (
            icons["repo"] if r["kind"] == "repo" else (
                icons["hist"] if r["kind"] == "hist" else icons["restore"]))
        st = r["state"]
        if box == "tint":
            inner = svg(pre, ic, 20, TINT_FG[st])
            ico = (f'<span class="ibox tint" style="background:{TINT[st]};color:{TINT_FG[st]}">{inner}</span>')
        elif box == "solid":
            inner = svg(pre, ic, 18, "#fff")
            ico = (f'<span class="ibox solid" style="background:{TINT_FG[st]}">{inner}</span>')
        else:
            ico = f'<span class="ibox plain">{svg(pre, ic, 26, TINT_FG[st])}</span>'
        pill = {"ok": ("正常", "ok"), "bad": ("失败", "bad"), "run": ("进行中", "run")}[st]
        rows.append(f'''<div class="row">
          {ico}
          <div class="rt"><b>{r["title"]}</b><small>{r["sub"]}</small></div>
          <span class="pill {pill[1]}">{pill[0]}</span>
        </div>''')
    strip = "".join(
        f'<div class="cell"><div class="ci">{svg(pre, icons[k], 30, s["color"] if box != "tint" else None)}</div><span>{lbl}</span></div>'
        for k, lbl in SLOTS)
    return f'''<section class="card">
      <header>
        <span class="kbd">{s["key"]}</span>
        <div class="hd"><h2>{s["name"]}</h2><p>{s["note"]}</p></div>
        <span class="lic">{s["tag"]}</span>
      </header>
      <div class="body">
        <div class="rows">{''.join(rows)}</div>
        <div class="strip">{strip}</div>
      </div>
    </section>'''


INVENTORY = [
    ("备份页 · 任务行", ".tt-ic", "36 × 36 / r8", "绿·橙·蓝·灰 50", "folder 20px", "状态色方块"),
    ("备份页 · 手机卡片", ".tc-icon", "40 × 40 / 无底", "无", "folder 22px", "图标 + 状态角标"),
    ("备份页 · 空状态", ".ov-empty-ic", "88 × 88 / r24", "blue-50", "shield-check 44px", "状态色方块"),
    ("快照页 · 仓库行", ".sn-ic", "44 × 44 / r10", "#eef4ff", "stack-2 21px", "固定蓝方块"),
    ("快照页 · 详情头", ".sd-ic", "52 × 52 / r12", "紫渐变", "stack-2 26px", "实色渐变"),
    ("快照页 · 空状态", ".empty-icon", "—", "无", "database 42px", "裸图标"),
    ("历史页 · 每条记录", ".hi-ic", "32 × 32 / r8", "绿·红·蓝·灰 50", "folder / arrow-back-up 16px", "状态色方块"),
    ("还原 · 选任务", ".rp-ic", "40 × 40 / r10", "绿·橙·蓝·灰 50", "folder 20px", "状态色方块"),
    ("还原 · 目的地", ".ic.c-blue / .c-green", "—", "blue-50 / green-50", "map-pin / folder 20px", "色块"),
    ("新建任务 · 来源", ".src-ico", "—", "无", "device-desktop 18px", "裸图标"),
    ("文件浏览 · 行", ".fold-ic", "—", "无", "folder / file 彩色", "图标自带蓝/灰"),
    ("侧栏 / 底部导航", ".sb-item", "—", "仅激活态", "folder / stack-2 / history 17px", "激活态浅底"),
]

REC = [
    ("列表行（任务 / 仓库 / 历史）", "D · Solar Bold Duotone", "去掉彩色方块，图标直接着色。状态只在右侧标签说一次，最干净。"),
    ("还原流程 / 目的地选择", "B · Phosphor Duotone", "双色调在大图标上比纯线性更有层次，20–24px 刚好。"),
    ("空状态 / 首次引导", "F · Soft 3D", "一个页面只出现一次，立体感是加分项，不构成干扰。"),
    ("导航 / 工具栏小图标", "A · Tabler（保留）", "16–17px 尺寸下双色会糊，线性反而最清楚。"),
]

COLOR_FOLLOW = [
    ("A · Tabler 线性", "✅ 跟随", "currentColor 描边", "全覆盖"),
    ("B · Phosphor Duotone", "✅ 跟随", "currentColor 填充 + 半透明层", "全覆盖"),
    ("C · Phosphor Fill", "✅ 跟随", "currentColor 实心", "全覆盖"),
    ("D · Solar Bold Duotone", "✅ 跟随", "currentColor 填充 + 淡色层", "全覆盖"),
    ("E · Streamline Color", "❌ 固定 #4147d5", "硬编码 fill/stroke", "缺历史、还原"),
    ("F · Soft 3D", "❌ 固定彩色", "渐变 + 光影", "全覆盖"),
    ("G · Fluent Emoji", "❌ 固定彩色", "渐变", "缺历史、还原、设备"),
]


def main():
    blocks = "".join(scheme_block(s) for s in SCHEMES)
    inv = "".join(
        f"<tr><td>{a}</td><td><code>{b}</code></td><td>{c}</td><td>{d}</td><td>{e}</td><td>{f}</td></tr>"
        for a, b, c, d, e, f in INVENTORY)
    rec = "".join(f"<tr><td>{a}</td><td><b>{b}</b></td><td>{c}</td></tr>" for a, b, c in REC)
    cf = "".join(f"<tr><td>{a}</td><td>{b}</td><td>{c}</td><td>{d}</td></tr>" for a, b, c, d in COLOR_FOLLOW)

    doc = f'''<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>量子密备 · 图标替换方案</title>
<style>
  *{{box-sizing:border-box;margin:0;padding:0}}
  body{{font-family:-apple-system,"PingFang SC","Helvetica Neue",Arial,sans-serif;
    background:#f5f6f8;color:#1f2329;-webkit-font-smoothing:antialiased;padding:40px 24px 72px}}
  .wrap{{max-width:1120px;margin:0 auto}}
  .top h1{{font-size:24px;letter-spacing:-.3px}}
  .top p{{color:#6b7280;font-size:13.5px;margin-top:8px;line-height:1.7}}
  .sec{{margin-top:38px}}
  .sec > h2{{font-size:15px;font-weight:600;margin-bottom:14px;display:flex;align-items:center;gap:8px}}
  .sec > h2::before{{content:"";width:3px;height:14px;border-radius:2px;background:#3370ff}}
  table{{width:100%;border-collapse:collapse;background:#fff;border-radius:12px;overflow:hidden;
    box-shadow:0 1px 2px rgba(16,24,40,.05);font-size:13px}}
  th{{text-align:left;font-weight:600;color:#6b7280;font-size:12px;background:#fafbfc;
    padding:10px 14px;border-bottom:1px solid #eef0f3}}
  td{{padding:10px 14px;border-bottom:1px solid #f2f4f7;color:#374151;vertical-align:middle}}
  tr:last-child td{{border-bottom:none}}
  code{{font-family:ui-monospace,Menlo,monospace;font-size:11.5px;color:#7b61ff;background:#f7f5ff;
    padding:1px 6px;border-radius:4px}}
  .card{{background:#fff;border-radius:14px;padding:20px 22px 22px;margin-bottom:16px;
    box-shadow:0 1px 2px rgba(16,24,40,.05)}}
  .card header{{display:flex;align-items:flex-start;gap:12px;margin-bottom:16px}}
  .kbd{{flex:none;width:24px;height:24px;border-radius:7px;background:#1f2329;color:#fff;
    font-size:12px;font-weight:700;display:flex;align-items:center;justify-content:center;margin-top:1px}}
  .hd h2{{font-size:15px;font-weight:600}}
  .hd p{{font-size:12.5px;color:#8a919e;margin-top:4px;line-height:1.6;max-width:720px}}
  .lic{{flex:none;margin-left:auto;font-size:11px;color:#6b7280;background:#f2f4f7;
    padding:3px 8px;border-radius:5px;white-space:nowrap}}
  .body{{display:flex;gap:22px;align-items:stretch}}
  .rows{{flex:1;min-width:0;display:flex;flex-direction:column;gap:6px;
    background:#fbfcfd;border:1px solid #f0f2f5;border-radius:10px;padding:8px}}
  .row{{display:flex;align-items:center;gap:11px;padding:8px 10px;background:#fff;border-radius:8px}}
  .ibox{{flex:none;display:flex;align-items:center;justify-content:center}}
  .ibox.tint{{width:34px;height:34px;border-radius:8px}}
  .ibox.solid{{width:32px;height:32px;border-radius:9px}}
  .ibox.plain{{width:34px;height:34px}}
  .rt{{flex:1;min-width:0}}
  .rt b{{display:block;font-size:13px;font-weight:600;color:#1f2329}}
  .rt small{{display:block;font-size:11.5px;color:#9aa1ad;margin-top:2px;
    white-space:nowrap;overflow:hidden;text-overflow:ellipsis}}
  .pill{{flex:none;font-size:11px;padding:2px 8px;border-radius:5px;font-weight:500}}
  .pill.ok{{background:#e8f8ee;color:#12a150}}
  .pill.bad{{background:#fdecec;color:#e5484d}}
  .pill.run{{background:#eaf1ff;color:#3370ff}}
  .strip{{flex:none;width:330px;display:grid;grid-template-columns:repeat(4,1fr);gap:10px 6px;
    align-content:center}}
  .cell{{display:flex;flex-direction:column;align-items:center;gap:6px}}
  .ci{{width:46px;height:46px;display:flex;align-items:center;justify-content:center}}
  .cell span{{font-size:11px;color:#9aa1ad;white-space:nowrap}}
  .miss{{display:inline-block;border:1px dashed #e5484d;border-radius:4px;font-size:9px;color:#e5484d;
    display:flex;align-items:center;justify-content:center}}
  .foot{{margin-top:8px;font-size:12px;color:#9aa1ad;line-height:1.8}}
  .rec td:first-child{{color:#6b7280;width:230px}}
</style></head>
<body><div class="wrap">
  <div class="top">
    <h1>量子密备 · 图标替换方案</h1>
    <p>先盘清线上所有「彩色浅底方块 + 线性图标」的位置，再给出 7 套可选图标体系，全部按真实行样式渲染。
       每套都能直接落到 <code>frontend/src/icons.js</code> 里，不用改组件结构。</p>
  </div>

  <div class="sec"><h2>一、现状盘点：12 处用了「浅底 / 色块 + 线性图标」</h2>
    <table><thead><tr><th>位置</th><th>容器</th><th>尺寸</th><th>底色</th><th>图标</th><th>做法</th></tr></thead>
    <tbody>{inv}</tbody></table>
    <p class="foot">共性问题：尺寸 32 / 36 / 40 / 44 / 52 五种混用，圆角 8 / 10 / 12 / 24 四种混用；
      同一件事（状态）在色块和右侧标签上各说一遍。</p>
  </div>

  <div class="sec"><h2>二、七套图标体系（同一组真实场景对比）</h2>
    <p class="foot" style="margin:-6px 0 14px">
      四行分别是：备份任务（正常）、快照仓库、历史记录（失败）、还原中。右侧是八个核心语义位的图标。
      注意彩色图标集（E/F/G）自带固定色，状态色在它们身上会失效——这是能否用于列表行的关键判据。
    </p>
  </div>
  {blocks}

  <div class="sec"><h2>三、关键判据：能不能跟随状态色</h2>
    <table><thead><tr><th>方案</th><th>跟随状态色</th><th>实现方式</th><th>语义覆盖</th></tr></thead>
    <tbody>{cf}</tbody></table>
    <p class="foot">这是能不能用在列表行上的分水岭。A–D 用 <code>currentColor</code>，
      状态一变图标跟着变；E–G 颜色写死在 SVG 里，成功/失败只能靠旁边的标签表达，
      而彩色图标自带的颜色还可能误导（比如 Soft 3D 的时钟是红的，会被误读成出错）。</p>
  </div>

  <div class="sec"><h2>四、落地成本</h2>
    <table><thead><tr><th>方案</th><th>改动范围</th><th>工作量</th></tr></thead><tbody>
      <tr><td>B / D（单色或双色、跟随 currentColor）</td>
          <td>只换 <code>icons.js</code> 里的路径表 + 删掉三处色块 CSS</td>
          <td>约 1 小时，模板零改动</td></tr>
      <tr><td>C（实心 + 实色底）</td>
          <td>换路径表 + 改 <code>.tt-ic / .hi-ic / .sn-ic / .rp-ic</code> 四处底色规则</td>
          <td>约 2 小时</td></tr>
      <tr><td>E / F / G（彩色图标）</td>
          <td>换路径表 + 移除所有 <code>color</code> 依赖 + 重新设计状态表达（色块不能再用）</td>
          <td>半天以上，且要逐页核对语义</td></tr>
    </tbody></table>
    <p class="foot">所有方案都不需要改组件结构：<code>Icon.vue</code> 用 <code>v-html</code> 注入路径，
      只要把 <code>icons.js</code> 里的 <code>stroke-width</code> / 路径换成对应图标集即可。
      彩色图标集要额外把 svg 的 <code>fill="none"</code> 去掉，否则部分填充图形会被裁掉。</p>
  </div>

  <div class="sec"><h2>五、我的建议：不用一套打到底，按位置分三档</h2>
    <table class="rec"><thead><tr><th>位置</th><th>用哪套</th><th>理由</th></tr></thead>
    <tbody>{rec}</tbody></table>
    <p class="foot">
      许可提醒：Solar 与 Streamline 是 CC BY 4.0，上架时必须在「关于」页或 README 里署名作者；
      Tabler / Phosphor / Soft 3D / Fluent Emoji 都是 MIT，无需署名。<br>
      覆盖度提醒：Streamline Color 与 Fluent Emoji 缺少「历史 / 还原」这类系统语义图标，
      只适合局部点缀，不能整套替换。
    </p>
  </div>
</div></body></html>'''
    out = os.path.join(HERE, "preview.html")
    open(out, "w", encoding="utf-8").write(doc)
    print("wrote", out, len(doc), "bytes")


if __name__ == "__main__":
    main()
