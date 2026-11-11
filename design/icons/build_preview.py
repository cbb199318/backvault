#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""根据 cache.json 生成图标选型预览页。"""
import os, json, html

HERE = os.path.dirname(os.path.abspath(__file__))
CACHE = json.load(open(os.path.join(HERE, "cache.json"), encoding="utf-8"))

from fetch_icons import FAMILIES, SLOTS

NORM = {"ph-bold": "ph", "ph-duotone": "ph", "ph-fill": "ph"}

FAMILY_META = {
    "ph": ("Phosphor", "MIT", "1.5px 线性，圆头收笔，气质最中性"),
    "ph-bold": ("Phosphor Bold", "MIT", "加粗到 2px，小尺寸更实"),
    "ph-duotone": ("Phosphor Duotone", "MIT", "同色两层，浅色填充 + 深色线，层次最好"),
    "solar": ("Solar", "CC BY 4.0", "双色描边，设计感最强，需署名"),
    "tabler": ("Tabler", "MIT", "2px 线性，工整硬朗，覆盖面最广"),
    "lucide": ("Lucide", "ISC", "1.5px 线性，最主流的开源风格"),
    "iconoir": ("Iconoir", "MIT", "几何感强，转角偏硬，辨识度高"),
    "mingcute": ("MingCute", "Apache 2.0", "圆润可爱，笔画均匀"),
    "material-symbols": ("Material Symbols", "Apache 2.0", "圆角、光学尺寸优化，安卓味"),
    "fluent": ("Fluent", "MIT", "微软体系，细节丰富，偏 20px 以上"),
    "ri": ("Remix Icon", "Apache 2.0", "国内项目常见，线条柔和"),
}

# 三套完整方案：每个槽位一个图标
SETS = [
    ("ph-duotone", "Phosphor Duotone", "同色双色填充，浅底深线，界面里层次最丰富，也不抢文字",
     ["folder-simple-duotone", "plus-circle-duotone", "cloud-arrow-up-duotone", "stack-duotone",
      "folder-open-duotone", "clock-counter-clockwise-duotone", "database-duotone", "list-checks-duotone",
      "shield-check-duotone", "warning-circle-duotone", "arrow-counter-clockwise-duotone",
      "magnifying-glass-duotone", "arrows-clockwise-duotone", "dots-three-duotone", "eye-duotone",
      "copy-duotone", "trash-duotone", "file-duotone", "desktop-duotone", "map-pin-duotone"]),
    ("solar", "Solar Bold Duotone", "描边 + 实色两层，设计感最强；CC BY 4.0，需在关于页署名",
     ["folder-bold-duotone", "add-circle-bold-duotone", "cloud-plus-bold-duotone", "layers-bold-duotone",
      "folder-open-bold-duotone", "history-bold-duotone", "database-bold-duotone", "checklist-bold-duotone",
      "shield-check-bold-duotone", "danger-circle-bold-duotone", "restart-bold-duotone",
      "magnifer-bold-duotone", "refresh-bold-duotone", "menu-dots-bold-duotone", "eye-bold-duotone",
      "copy-bold-duotone", "trash-bin-trash-bold-duotone", "file-bold-duotone", "monitor-bold-duotone",
      "map-point-bold-duotone"]),
    ("tabler", "Tabler 2px", "统一 2px 描边，克制中性，最不容易过时，也最省心",
     ["folder", "folder-plus", "cloud-plus", "stack-2", "folder-open", "history", "database",
      "list-check", "shield-check", "alert-circle", "arrow-back-up", "search", "refresh", "dots",
      "eye", "copy", "trash", "file", "device-desktop", "map-pin"]),
]

# 语境演示里各库对应的图标名
CTX = {
    "ph-duotone": {"folder": "folder-simple-duotone", "snap": "stack-duotone",
                   "hist": "clock-counter-clockwise-duotone", "db": "database-duotone",
                   "list": "list-checks-duotone", "clock": "clock-counter-clockwise-duotone",
                   "add": "plus-circle-duotone", "cloud": "cloud-arrow-up-duotone",
                   "browse": "folder-open-duotone"},
    "tabler": {"folder": "folder", "snap": "stack-2", "hist": "history", "db": "database",
               "list": "list-check", "clock": "clock", "add": "plus", "cloud": "cloud-plus",
               "browse": "folder-open"},
    "lucide": {"folder": "folder", "snap": "layers", "hist": "rotate-ccw-clock", "db": "database",
               "list": "list-checks", "clock": "clock", "add": "plus", "cloud": "cloud-upload",
               "browse": "folder-open"},
}

# 当前 App.vue 里在用的图标（手绘路径）
CURRENT = [
    ("备份任务", '<path fill="currentColor" d="M4 5.5A2.5 2.5 0 0 1 6.5 3h3.67a2 2 0 0 1 1.41.59l1.42 1.41H19.5A2.5 2.5 0 0 1 22 7.5v10a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 4 17.5v-12Z"/>', 24, 24),
    ("快照", '<path fill="currentColor" d="M12 2.8 2.6 7.7 12 12.6l9.4-4.9L12 2.8ZM2.6 11.9l9.4 4.9 9.4-4.9-2.2-1.2L12 14.2l-7.2-3.5-2.2 1.2Zm0 4.3 9.4 4.9 9.4-4.9-2.2-1.2L12 18.5l-7.2-3.5-2.2 1.2Z"/>', 24, 24),
    ("历史", '<path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" d="M12 7.2v5l3.1 1.9M20.6 12a8.6 8.6 0 1 1-2.5-6.1"/>', 24, 24),
    ("新增", '<path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" fill="none"/>', 24, 24),
    ("成功率", '<path fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" d="M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6z"/><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="m8.8 12 2.3 2.3 4.2-4.6"/>', 24, 24),
    ("云端仓库", '<g fill="none" stroke="currentColor" stroke-width="1.8"><ellipse cx="12" cy="6" rx="7.5" ry="2.8"/><path d="M4.5 6v12c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8V6M4.5 12c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8"/></g>', 24, 24),
]


def svg(prefix, name, size=22, cls="ic"):
    k = f"{NORM.get(prefix, prefix)}:{name}"
    d = CACHE.get(k)
    if not d:
        return f'<span class="miss" title="{k}">?</span>'
    return (f'<svg class="{cls}" viewBox="0 0 {d["w"]} {d["h"]}" width="{size}" height="{size}" '
            f'aria-hidden="true">{d["body"]}</svg>')


def esc(s):
    return html.escape(str(s))


def build():
    parts = []
    A = parts.append

    A("""<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>量子密备 · 图标选型</title>
<style>
:root{--blue:#3370ff;--blue-l:#eaf1ff;--ink:#1b2233;--sub:#8b93a7;--line:#e8ebf2;--bg:#f4f6fa;--card:#fff}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);
  font:14px/1.6 -apple-system,BlinkMacSystemFont,"PingFang SC","Helvetica Neue",Arial,sans-serif;
  padding:36px 40px 120px}
h1{font-size:24px;margin:0 0 8px;letter-spacing:.3px}
.lead{color:var(--sub);max-width:900px;margin:0 0 6px}
.lead b{color:var(--ink);font-weight:600}
h2{font-size:17px;margin:44px 0 4px;display:flex;align-items:center;gap:10px}
h2 .n{display:inline-flex;width:24px;height:24px;border-radius:8px;background:var(--blue);
  color:#fff;font-size:13px;align-items:center;justify-content:center;font-weight:600}
.hint{color:var(--sub);font-size:13px;margin:0 0 18px}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;
  box-shadow:0 1px 2px rgba(20,30,60,.04);padding:20px}
.row{display:flex;gap:14px;flex-wrap:wrap}

/* 现状 */
.now{display:flex;gap:12px;flex-wrap:wrap}
.now .it{width:104px;padding:14px 8px;border:1px dashed #d7dded;border-radius:12px;text-align:center;color:#5a6478}
.now .it small{display:block;color:var(--sub);font-size:11px;margin-top:6px}
.now .it .ic{opacity:.85}

/* 整套风格 */
.fam{display:grid;grid-template-columns:190px 1fr 250px;gap:18px;align-items:center;
  padding:14px 6px;border-bottom:1px solid #f1f3f8}
.fam:last-child{border-bottom:0}
.fam .nm{font-weight:600}
.fam .nm small{display:block;font-weight:400;color:var(--sub);font-size:11.5px;margin-top:2px}
.fam .lic{display:inline-block;margin-top:6px;font-size:10.5px;color:#7b849b;background:#f2f4f9;
  border-radius:5px;padding:1px 6px}
.fam .set{display:flex;gap:22px;align-items:center}
.fam .cell{display:flex;flex-direction:column;align-items:center;gap:6px;width:56px}
.fam .cell span{font-size:11px;color:var(--sub)}
.fam .set .ic{color:#3b4559}
.fam .mock{background:#f7f9fc;border:1px solid #eef1f7;border-radius:10px;padding:8px 10px;
  display:flex;gap:6px}
.fam .mock i{flex:1;display:flex;flex-direction:column;align-items:center;gap:3px;
  font-style:normal;font-size:10.5px;color:#6b7488;padding:6px 0;border-radius:8px}
.fam .mock i.on{background:var(--blue-l);color:var(--blue)}

/* 槽位候选 */
.slots{display:grid;grid-template-columns:repeat(auto-fill,minmax(430px,1fr));gap:16px}
.set-head{display:flex;align-items:center;justify-content:space-between;
  padding:2px 2px 14px;border-bottom:1px solid #f1f3f8;margin-bottom:14px}
.set-head b{font-size:14.5px}
.set-head span{display:block;color:var(--sub);font-size:12px;margin-top:3px}
.set-head .set-tag{display:inline-block;font-size:11px;color:#fff;background:var(--blue);
  border-radius:6px;padding:2px 9px;margin:0;line-height:1.5}
.set-head .set-tag:empty{display:none}
.set-grid{display:grid;grid-template-columns:repeat(10,1fr);gap:6px}
.sg{display:flex;flex-direction:column;align-items:center;gap:7px;padding:12px 4px;border-radius:10px;
  color:#3b4559}
.sg:hover{background:#f7f9fd}
.sgl{font-size:10.5px;color:var(--sub);text-align:center;line-height:1.3}
.slot{background:#fff;border:1px solid var(--line);border-radius:14px;padding:16px 16px 12px}
.slot-h{display:flex;align-items:baseline;gap:8px;margin-bottom:12px}
.slot-h b{font-size:14.5px}
.slot-h span{font-size:11.5px;color:var(--sub)}
.cands{display:grid;grid-template-columns:repeat(3,1fr);gap:8px}
.cand{border:1px solid #edf0f6;background:#fbfcfe;border-radius:11px;padding:10px 6px 8px;
  cursor:pointer;text-align:center;font:inherit;color:#3b4559;transition:.15s}
.cand:hover{border-color:#c9d7f8;background:#f5f8ff;transform:translateY(-1px)}
.cand.picked{border-color:var(--blue);background:var(--blue-l);box-shadow:0 0 0 2px rgba(51,112,255,.12)}
.cand .ic{margin-bottom:6px}
.cand .nm{display:block;font-size:11px;color:#5a6478;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.cand .cd{display:block;font-size:9.5px;color:#a8afc0;font-family:ui-monospace,Menlo,monospace}
.cand.picked .cd{color:var(--blue)}

/* 语境 */
.ctx{display:grid;grid-template-columns:1fr 1fr;gap:18px}
.demo{background:#fff;border:1px solid var(--line);border-radius:16px;padding:0;overflow:hidden}
.demo h4{margin:0;padding:12px 16px;font-size:12.5px;color:var(--sub);font-weight:500;
  border-bottom:1px solid #f1f3f8;background:#fafbfe}
.demo .body{padding:16px;display:flex;gap:22px}
.sb{width:150px;background:#fbfcfe;border:1px solid #eef1f7;border-radius:12px;padding:8px}
.sb a{display:flex;align-items:center;gap:9px;padding:9px 10px;border-radius:9px;color:#5a6478;
  text-decoration:none;font-size:12.5px}
.sb a.on{background:var(--blue-l);color:var(--blue);font-weight:600}
.sb a .ic{color:currentColor}
.kpis{display:grid;grid-template-columns:1fr 1fr;gap:10px;flex:1}
.kpi{border:1px solid #eef1f7;border-radius:11px;padding:10px 12px;display:flex;align-items:center;gap:10px}
.kpi .kb{width:34px;height:34px;border-radius:10px;display:grid;place-items:center}
.kpi .kb.b{background:#eaf1ff;color:#3370ff}
.kpi .kb.g{background:#e8f8ef;color:#12a45a}
.kpi .kb.p{background:#f0ecff;color:#7a5af8}
.kpi .kb.o{background:#fff1e6;color:#e8890c}
.kpi b{display:block;font-size:15px;line-height:1.2}
.kpi span{font-size:11px;color:var(--sub)}
.btns{display:flex;gap:10px;flex-wrap:wrap;margin-top:14px}
.btns .b1{display:inline-flex;align-items:center;gap:7px;background:var(--blue);color:#fff;
  border-radius:9px;padding:8px 14px;font-size:12.5px;font-weight:500}
.btns .b2{display:inline-flex;align-items:center;gap:7px;background:#fff;color:#3b4559;
  border:1px solid #e2e7f0;border-radius:9px;padding:8px 14px;font-size:12.5px}

/* 尺寸 */
.sizes{display:flex;gap:12px;align-items:center;padding:2px 0}
.szg{display:flex;align-items:center;gap:10px;border:1px solid #f0f3f8;border-radius:10px;
  padding:9px 12px;background:#fbfcfe}
.szg .szi{display:inline-flex;align-items:center;gap:12px}
.szg small{color:var(--sub);font-size:10.5px}

/* 底部选择栏 */
.bar{position:fixed;left:0;right:0;bottom:0;background:rgba(255,255,255,.92);backdrop-filter:blur(12px);
  border-top:1px solid var(--line);padding:12px 40px;display:flex;align-items:center;gap:14px;font-size:12.5px}
.bar .lbl{color:var(--sub)}
.bar #picks{flex:1;color:#3b4559;font-family:ui-monospace,Menlo,monospace;font-size:12px;
  max-height:38px;overflow:auto}
.bar button{font:inherit;border:1px solid #e2e7f0;background:#fff;border-radius:8px;padding:6px 12px;cursor:pointer}
.bar button.pri{background:var(--blue);color:#fff;border-color:var(--blue)}
.miss{display:inline-grid;place-items:center;width:22px;height:22px;border-radius:6px;background:#ffe9e9;color:#d33;font-size:11px}
@media(max-width:900px){body{padding:20px 16px 120px}.fam{grid-template-columns:1fr}.fam .mock{display:none}
  .ctx{grid-template-columns:1fr}.slots{grid-template-columns:1fr}}
</style></head><body>""")

    A('<h1>量子密备 · 图标选型</h1>')
    A('<p class="lead">下面这些全部是开源图标库的真实图标，按你的界面槽位排好了：'
      '<b>先看第 1 节挑整体风格</b>（一套用下来最统一），再看第 2 节微调个别图标。'
      '点任意图标可以标记，底部会汇总你的选择。</p>')
    A('<p class="lead" style="font-size:12.5px">来源：Phosphor / Solar / Tabler / Lucide / Iconoir / MingCute / '
      'Material Symbols / Fluent / Remix，均为可商用的开源许可（Solar 为 CC BY 4.0，需在关于页署名）。</p>')

    # 0 现状
    A('<h2><span class="n">0</span>现在用的</h2>')
    A('<p class="hint">当前是手绘路径，线条粗细不统一，缩到 16px 会糊；下面每节都拿来对照。</p>')
    A('<div class="card"><div class="now">')
    for label, body, w, h in CURRENT:
        A(f'<div class="it"><svg class="ic" viewBox="0 0 {w} {h}" width="26" height="26">{body}</svg>'
          f'<small>{esc(label)}</small></div>')
    A('</div></div>')

    # 1 整套
    A('<h2><span class="n">1</span>整套风格对比</h2>')
    A('<p class="hint">每行是一套库的四个主图标（备份 / 快照 / 历史 / 新增），右边是放进侧栏的样子。'
      '同一套库整套替换，视觉最统一。</p>')
    A('<div class="card" style="padding:8px 18px">')
    for prefix, label, names in FAMILIES:
        fam, lic, desc = FAMILY_META.get(prefix, (label, "", ""))
        A('<div class="fam">')
        A(f'<div class="nm">{esc(fam)}<small>{esc(desc)}</small>'
          f'<span class="lic">{esc(lic)}</span></div>')
        A('<div class="set">')
        for nm, cn in zip(names, ["备份", "快照", "历史", "新增"]):
            A(f'<div class="cell">{svg(prefix, nm, 26)}<span>{cn}</span></div>')
        A('</div>')
        A('<div class="mock">')
        for nm, cn, on in zip(names, ["备份", "快照", "历史", "新增"], [True, False, False, False]):
            A(f'<i class="{"on" if on else ""}">{svg(prefix, nm, 17)}<span>{cn}</span></i>')
        A('</div></div>')
    A('</div>')

    # 2 完整套装
    A('<h2><span class="n">2</span>三套完整方案</h2>')
    A('<p class="hint">同一套库铺满全部 20 个位置的样子，直接看哪一套顺眼，报「方案 A / B / C」就行。</p>')
    for idx, (pref, title, desc, names) in enumerate(SETS):
        A('<div class="card" style="margin-bottom:16px">')
        A(f'<div class="set-head"><div><b>方案 {"ABC"[idx]} · {esc(title)}</b>'
          f'<span>{esc(desc)}</span></div>'
          f'<span class="set-tag">{"推荐" if idx == 0 else ""}</span></div>')
        A('<div class="set-grid">')
        for (st, _w, _c), nm in zip(SLOTS, names):
            A(f'<div class="sg"><span class="sgi">{svg(pref, nm, 24)}</span>'
              f'<span class="sgl">{esc(st)}</span></div>')
        A('</div></div>')

    # 3 逐项
    A('<h2><span class="n">3</span>逐项候选</h2>')
    A('<p class="hint">每格 6 个候选，来自不同库；编号形如 1-3，方便你直接报给我。</p>')
    A('<div class="slots">')
    for si, (title, where, cands) in enumerate(SLOTS, 1):
        A('<div class="slot">')
        A(f'<div class="slot-h"><b>{si}. {esc(title)}</b><span>{esc(where)}</span></div>')
        A('<div class="cands">')
        for ci, (p, n) in enumerate(cands, 1):
            code = f"{si}-{ci}"
            fam = FAMILY_META.get(p, (p, "", ""))[0]
            A(f'<button class="cand" data-code="{code}" data-icon="{esc(p)}:{esc(n)}" '
              f'data-label="{esc(title)} · {esc(fam)}">'
              f'{svg(p, n, 22)}<span class="nm">{esc(fam)}</span>'
              f'<span class="cd">{code}</span></button>')
        A('</div></div>')
    A('</div>')

    # 3 语境
    A('<h2><span class="n">4</span>放进界面的样子</h2>')
    A('<p class="hint">左边 Phosphor Duotone（双色，我推荐），右边 Tabler（2px 线性，最稳）。</p>')
    A('<div class="ctx">')
    for pref, tit in (("ph-duotone", "Phosphor Duotone"), ("tabler", "Tabler 2px")):
        A(f'<div class="demo"><h4>{tit}</h4><div class="body">')
        A('<div class="sb">')
        m = CTX[pref]
        for nm, cn, on in ((m["folder"], "备份任务", True),
                           (m["snap"], "快照", False),
                           (m["hist"], "历史", False)):
            A(f'<a class="{"on" if on else ""}">{svg(pref, nm, 18)}<span>{cn}</span></a>')
        A('</div>')
        A('<div style="flex:1">')
        A('<div class="kpis">')
        for cls, ic, val, cn in (("b", m["db"], "3", "云端仓库"),
                                 ("p", m["snap"], "128", "快照总数"),
                                 ("g", m["list"], "5", "关联任务"),
                                 ("o", m["clock"], "11-09", "最近快照")):
            A(f'<div class="kpi"><span class="kb {cls}">{svg(pref, ic, 19)}</span>'
              f'<div><b>{val}</b><span>{cn}</span></div></div>')
        A('</div>')
        A('<div class="btns">')
        A(f'<span class="b1">{svg(pref, m["add"], 16)}<span>创建任务</span></span>')
        A(f'<span class="b2">{svg(pref, m["cloud"], 16)}<span>新增云端</span></span>')
        A(f'<span class="b2">{svg(pref, m["browse"], 16)}<span>浏览目录</span></span>')
        A('</div></div></div></div>')
    A('</div>')

    # 4 尺寸
    A('<h2><span class="n">5</span>小尺寸检查</h2>')
    A('<p class="hint">桌面侧栏是 17px，手机底部导航 22px，KPI 是 20px。下面是三套在 28 / 20 / 16 下的样子。</p>')
    A('<div class="card">')
    for pref, tit in (("ph-duotone", "Phosphor Duotone"), ("tabler", "Tabler"), ("lucide", "Lucide")):
        A(f'<div style="display:flex;align-items:center;gap:22px;padding:10px 0;border-bottom:1px solid #f3f5fa">'
          f'<span style="width:150px;color:#6b7488;font-size:12.5px">{tit}</span>')
        A('<div class="sizes">')
        mm = CTX[pref]
        for sz in (28, 20, 16):
            A(f'<div class="szg"><span class="szi" style="color:#3b4559">'
              f'{svg(pref, mm["folder"], sz)}{svg(pref, mm["snap"], sz)}{svg(pref, mm["hist"], sz)}'
              f'</span><small>{sz}px</small></div>')
        A('</div></div>')
    A('</div>')

    A("""
<div class="bar">
  <span class="lbl">已选：</span><div id="picks">（点上面的图标来选）</div>
  <button id="copy">复制选择</button>
  <button id="clear">清空</button>
</div>
<script>
const picks = new Map();
const out = document.getElementById('picks');
function render(){
  if(!picks.size){ out.textContent = '（点上面的图标来选）'; return; }
  out.textContent = [...picks.entries()].map(([c,v]) => c + ' ' + v.label).join('  |  ');
}
document.querySelectorAll('.cand').forEach(b => {
  b.addEventListener('click', () => {
    const c = b.dataset.code;
    if(picks.has(c)){ picks.delete(c); b.classList.remove('picked'); }
    else { picks.set(c, {label:b.dataset.label, icon:b.dataset.icon}); b.classList.add('picked'); }
    render();
  });
});
document.getElementById('clear').onclick = () => {
  picks.clear(); document.querySelectorAll('.cand.picked').forEach(e=>e.classList.remove('picked')); render();
};
document.getElementById('copy').onclick = async () => {
  const txt = [...picks.entries()].map(([c,v]) => c + '\\t' + v.label + '\\t' + v.icon).join('\\n') || '（未选择）';
  try { await navigator.clipboard.writeText(txt); out.textContent = '已复制到剪贴板'; setTimeout(render, 1200); }
  catch(e){ out.textContent = txt; }
};
</script></body></html>""")

    out = os.path.join(HERE, "preview.html")
    open(out, "w", encoding="utf-8").write("\n".join(parts))
    print("written", out, os.path.getsize(out), "bytes")


if __name__ == "__main__":
    build()
