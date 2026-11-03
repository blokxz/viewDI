import os; exec(open(os.path.join(os.path.dirname(__file__), "svgmock.py")).read())
# UI proposals for the post-1.0 polish. Nothing here is built: it is for the user to react to.
W=120
def header(active, tabs=("1 Home","2 Machines","3 Library","4 Activity"), right="{dim:ctrl+k  do anything}  {dim:? help}"):
    t="  "+"  ".join(("{B:[%s]}"%x if x.startswith(active) else "{dim:%s}"%x) for x in tabs)
    return [pad(" {G:viewDI}",W-37)+"{G:●} Docker 29.1.3  {G:●} Incus 6.0.5  ",
            pad(t,W-vis(right)-1)+right+" "]
def card(title,lines,w,c="dim"):
    out=["{%s:╭─} {B:%s} {%s:%s╮}"%(c,title,c,"─"*(w-5-len(title)))]
    for l in lines: out.append("{%s:│} "%c+pad(l,w-4)+" {%s:│}"%c)
    out.append("{%s:╰%s╯}"%(c,"─"*(w-2)))
    return out
def beside(*blocks,gap=2):
    n=max(len(b) for b in blocks); out=[]
    for i in range(n):
        out.append((" "*gap).join(pad(b[i] if i<len(b) else "",max(vis(x) for x in b)) for b in blocks))
    return out


def mini(title,lines,w,c="dim",hot=None):
    c=hot or c
    return card(title,lines,w,c)
def apps(rows): return ["  "+l for l in rows]
STATUS=["",
"  {O:⚠ 1 thing needs you}   {dim:12 apps · 31 machines · 29 up · 1 starting · 1 stopped on purpose}","",
"  {O:⚠}  {B:web-db}   memory above 90% for 10 minutes                               {B:m} raise the limit   {B:l} logs",
"  {Y:⬆}  {B:Docker}   29.1.3 → 29.2.0 · security fix                                {B:u} upgrade now       {B:z} later",""]
# 1 · cards, overflow
C=[mini("web-stack",["{O:⚠} web-db memory 92%","4 machines","{dim:cpu}{G:▂▃▂▃▅}  {dim:mem}{O:▃▅▆▇}"],29,hot="O"),
   mini("k8s lab",["{Y:◐} 1 of 3 starting","k3s-1  k3s-2  k3s-3","{dim:cpu}{Y:▂▅▇▇▆}  {dim:mem}{Y:▃▄▅▆}"],29,hot="Y"),
   mini("nextcloud",["{G:●} up · 1 machine","{A:localhost:8085}","{dim:cpu}{G:▁▁▂▁▁}  {dim:mem}{G:▄▄▄▄▄}"],29),
   mini("monitoring",["{G:●} up · 2 machines","prometheus  grafana","{dim:cpu}{G:▁▂▁▁▂}  {dim:mem}{G:▃▃▄▄▄}"],29),
   mini("media",["{G:●} up · 3 machines","jellyfin  sonarr  radarr","{dim:cpu}{G:▂▂▃▂▂}  {dim:mem}{G:▅▅▅▆▆}"],29),
   mini("home",["{G:●} up · 2 machines","home-assistant  mqtt","{dim:cpu}{G:▁▁▁▁▁}  {dim:mem}{G:▃▃▃▃▃}"],29)]
r1=beside(*C[:3],gap=1); r2=beside(*C[3:],gap=1)
p1=STATUS+["  {B:APPS}  {dim:12 · needs attention first}"]+apps(r1)+apps(r2)+["  {dim:▼ 6 more apps, all healthy   (↓ scrolls · enter opens one in Machines · d density)}","",
pad(" {B:tab} into the apps  {B:↑↓←→} move  {B:enter} open  {B:d} density: cards  {B:/} find",118)]
P1=header("1")+box("Home",p1,W)

# 2 · compact rows, two columns
def rowa(sym,name,n,info,usage): return cols(["  "+sym+" "+"{B:"+name+"}"+" "*(14-len(name)),"{dim:%d}"%n+" "*3,info,usage],[4+14+1,5,22,9])
L=[rowa("{O:⚠}","web-stack",4,"web-db memory 92%","{O:▅} 1.4G"),
   rowa("{Y:◐}","k8s lab",3,"1 of 3 starting","{Y:▇} 3.1G"),
   rowa("{G:●}","nextcloud",1,"{A:localhost:8085}","{G:▂} 610M"),
   rowa("{G:●}","monitoring",2,"all up","{G:▂} 740M"),
   rowa("{G:●}","media",3,"all up","{G:▃} 2.2G"),
   rowa("{G:●}","home",2,"all up","{G:▁} 380M")]
R=[rowa("{G:●}","backups",1,"all up","{G:▁} 90M"),
   rowa("{G:●}","git",2,"all up","{G:▁} 320M"),
   rowa("{G:●}","dns",2,"all up","{G:▁} 120M"),
   rowa("{G:●}","dev",3,"all up","{G:▃} 1.1G"),
   rowa("{G:●}","iot",2,"all up","{G:▁} 210M"),
   rowa("{dim:○}","legacy",1,"stopped on purpose","{dim:·}")]
rows=[]
for a,b in zip(L,R): rows.append("  "+pad(a,56)+"  "+pad(b,56))
p2=STATUS+["  {B:APPS}  {dim:12 · two columns}"]+rows+["",
pad(" {B:tab} into the apps  {B:↑↓←→} move  {B:enter} open  {B:d} density: rows  {B:/} find",118)]
P2=header("1")+box("Home",p2,W)

# 3 · mosaic: one cell per machine
def cells(s): 
    m={"u":"{G:■}","s":"{Y:◐}","x":"{R:✖}","o":"{dim:□}"}
    return " ".join(m[c] for c in s)
mos=[("web-stack","{O:■} {G:■ ■ ■}","web-db is hot"),("k8s lab","{Y:◐} {G:■ ■}","1 starting"),("nextcloud","{G:■}",""),("monitoring","{G:■ ■}",""),
 ("media","{G:■ ■ ■}",""),("home","{G:■ ■}",""),("backups","{G:■}",""),("git","{G:■ ■}",""),("dns","{G:■ ■}",""),("dev","{G:■ ■ ■}",""),("iot","{G:■ ■}",""),("legacy","{dim:□}","stopped on purpose")]
rowsm=[]
col1=mos[:6]; col2=mos[6:]
for a,b in zip(col1,col2):
    rowsm.append("  "+pad("{B:"+a[0]+"}"+" "*(12-len(a[0]))+a[1],36)+"{dim:"+pad(a[2],16)+"}"+"  "+pad("{B:"+b[0]+"}"+" "*(12-len(b[0]))+b[1],36)+"{dim:"+b[2]+"}")
rowsm=[pad(r,118) for r in rowsm]
p3=STATUS+["  {B:APPS}  {dim:12 apps · one cell per machine · ■ up  ◐ starting  ✖ down  □ off on purpose}"]+rowsm+["",
"  {B:web-stack}  {dim:selected} · web-db {O:■} memory 92% · web-api · redis-cache · nginx-proxy",
"",pad(" {B:tab} into the apps  {B:↑↓←→} move  {B:enter} open  {B:d} density: mosaic  {B:/} find",118)]
P3=header("1")+box("Home",p3,W)

s=SVG(1060)
s.heading("Cards while they fit, then fewer lines per app, then one cell per machine","The Home never outgrows the screen: it shows less per app instead. d picks the density; by default the roomiest that fits.")
s.heading("1 · Cards (120×40): up to 6 apps in view, the ones that need you first","The rest are one line: how many, and that they are fine. ↓ scrolls the grid.")
s.term(P1,W,"viewdi — Home · cards",marks=[(10,0),(13,0),(23,0)])
s.notes([(1,"Order: attention first (alerts, starting, stopped by surprise), then busiest, then alphabetical. a pinned app (p) stays on top."),
 (2,"Two rows of cards fit in a 40-line terminal. More apps scroll; the grid is navigable with the arrow keys."),
 (3,"Healthy overflow is one sentence, not more cards: if they are fine, they do not need your eyes.")])
s.heading("2 · Rows (≥ 7 apps or a short terminal): one line per app, two columns","12 apps in 6 lines. In 80 columns it is one column of rows; in 80×24 only the lines that fit, with the overflow sentence.")
s.term(P2,W,"viewdi — Home · rows",marks=[(11,0),(11,60)])
s.notes([(1,"Same information as the card, in one line: health symbol, name, machines, what is wrong or how to reach it, usage."),
 (2,"Two columns at 120+, one below. The line count adapts to the free height.")])
s.heading("3 · Mosaic (a lab with dozens of machines): one cell per machine","31 machines in 6 lines. A colour per machine, grouped by app; the selected app expands below.")
s.term(P3,W,"viewdi — Home · mosaic",marks=[(11,0),(18,0)])
s.notes([(1,"Cells are the machine states, so a problem is visible at a glance even with 100+ machines."),
 (2,"The selected app lists its machines and what is wrong; enter opens it in Machines already filtered.")])
s.out(sys.argv[1])
