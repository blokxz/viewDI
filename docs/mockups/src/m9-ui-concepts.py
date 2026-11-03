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

# ───────────────────────── Concept 1: Home ─────────────────────────
c1=card("web-stack",["{G:●} all up  · 4 machines","web-api  web-db  redis","nginx-proxy","","{dim:cpu}  {G:▂▃▂▃▅▃▂}  3%","{dim:mem}  {G:▃▃▄▄▄▅▅}  1.4G"],30)
c2=card("nextcloud",["{G:●} up · 1 machine","","{A:http://localhost:8085}","","{dim:cpu}  {G:▁▁▂▁▁▁▂}  1%","{dim:mem}  {G:▄▄▄▄▄▄▄}  610M"],30)
c3=card("k8s lab",["{Y:◐} 1 of 3 starting","k3s-1  k3s-2  k3s-3","","","{dim:cpu}  {Y:▂▅▇▇▆▅▃}  38%","{dim:mem}  {Y:▃▄▅▆▆▆▇}  3.1G"],30)
cards=beside(c1,c2,c3)
home=["",
"  {G:✓ Everything that should be running is running.}   {dim:12 up · 1 stopped on purpose · all images current}","",
"  {B:NEEDS YOU}  {dim:3}",
"  {O:⚠}  {B:web-db}        memory has been above 90% for 10 minutes                 {B:m} raise the limit    {B:l} logs",
"  {Y:⬆}  {B:Docker}        29.1.3 → 29.2.0 · security fix                          {B:u} upgrade now        {B:z} later",
"  {R:✖}  {B:legacy-vm}     stopped since Sunday 14:02 (not by viewDI)               {B:s} start it           {B:i} it is fine",
"",
"  {B:YOUR APPS}   {dim:compose projects, labs and library entries, one card each}"]
home+=["  "+l for l in cards]
home+=["",
"  {B:START SOMETHING}   {B:n} new machine    {B:3} from the library: {A:PostgreSQL}  {A:Grafana}  {A:Debian + Docker}  {dim:…}","",
pad(" {B:↑↓} choose  {B:enter} do the first action  {B:tab} next card  {B:ctrl+k} do anything  {B:2} all machines",118)]
H1=header("1")+box("Home",home,W)

# ───────────────────────── Concept 2: Machines ─────────────────────────
lst=["{dim:  GROUP ◂ app ▸     VIEW ◂ list ▸     / filter}","",
"{B:▾ web-stack}  {dim:4 machines}",
"▶ {G:●} web-api        {dim:docker}  {A::8080}      {G:▂▃▂}  3%   412M",
"  {G:●} web-db         {dim:docker}  {O::5432}      {O:▅▆▇}  92%  1.9G",
"  {G:●} redis-cache    {dim:docker}  {dim:internal}  {G:▁▁▁}  1%   38M",
"  {G:●} nginx-proxy    {dim:docker}  {A::80 :443}   {G:▁▂▁}  2%   21M",
"{B:▾ k8s lab}  {dim:3 machines}",
"  {Y:◐} k3s-1          {dim:incus vm}  {dim:starting…}",
"  {G:●} k3s-2          {dim:incus vm}  {A:10.91.227.31}  {G:▃▄▃}  12%  1.2G",
"  {G:●} k3s-3          {dim:incus vm}  {A:10.91.227.32}  {G:▃▃▃}  9%   1.1G",
"{B:▸ nextcloud}  {dim:1 machine}",
"{B:▸ alone}  {dim:5 machines}"]
det=["{B:web-api}","{G:● running}  {dim:3 days · docker · from nginx:1.27}","",
"{dim:Reach it}    {A:http://localhost:8080}    {B:b}",
"{dim:Uses}        3% of one CPU · 412 MiB",
"{dim:Talks to}    web-db · redis-cache",
"{dim:Healthy}     last check ok 12 s ago","",
"{dim:What changed}","  2 d ago   restarted by you",
"  5 d ago   image updated","",
"{dim:Needs}       nothing"]
rows=[]
for i in range(max(len(lst),len(det))):
    g=lambda L:L[i] if i<len(L) else ""
    rows.append(cols([g(lst),"{dim:│} "+g(det)],[62,56]))
rows+=["",pad(" {B:enter} actions  {B:b} browser  {B:l} logs  {B:t} shell  {B:r} restart  {B:x} stop  {B:space} pick several  {B:g} regroup  {B:v} list/tree",118)]
H2=header("2")+box("Machines · 12",rows,W)

# ───────────────────────── Concept 3: Activity ─────────────────────────
act=["{dim:  SHOW ◂ everything ▸   alerts · updates · jobs · history}","",
"  {B:NOW}",
"  {O:⚠}  14:22  {B:web-db} memory 92% for 10 min                                    {B:m} raise limit   {B:l} logs",
"  {B:◐}  14:21  Creating {B:postgres-2}   pulling ▕██████░░░░▏ 62%                        {B:c} cancel",
"",
"  {B:TODAY}",
"  {G:✓}  14:02  Restarted {B:web-api}                                                      {dim:by you}",
"  {Y:⬆}  09:00  Docker 29.2.0 is out · security fix                                 {B:u} upgrade   {B:z} later",
"  {Y:⬆}  09:00  3 images have a newer build: nginx:1.27, redis:7, grafana:11       {B:p} pull   {B:c} recreate",
"  {R:✖}  08:12  {B:legacy-vm} stopped                                                      {dim:outside viewDI}",
"",
"  {B:EARLIER}",
"  {G:✓}  Sun   Created {B:nextcloud} from the library · open it with {B:b}",
"  {G:✓}  Sat   Removed 3 unused images · 723 MiB freed",
"",
pad(" {B:↑↓} choose  {B:enter} do the first action  {B:/} filter  {B:tab} change what is shown",118)]
H3=header("4")+box("Activity · 2 now",act,W)

s=SVG(1060)
s.heading("Proposal A · four tabs, and a Home that answers “is everything fine?”","Admin → Home · Table + Tree → Machines · Jobs + Updates + alerts → Activity · Library stays · Resources becomes a column.")
s.term(H1,W,"viewdi — Home",sel=[(7,2,118)],marks=[(4,0),(6,0),(7,0),(12,0),(21,0)])
s.notes([(1,"One sentence of status, in words. When it is not true, the sentence says what is not (“1 of 3 starting”)."),
 (2,"NEEDS YOU: only things a person must decide, each with the one or two actions that fix it. enter does the first one."),
 (3,"Metrics, alerts and updates are shown as consequences, not as separate screens to check."),
 (4,"Apps: compose projects, labs and library entries become a card each. Health and usage of the whole, not of each container."),
 (5,"Starting something: new machine or straight from the library. ctrl+k reaches anything.")])
s.heading("Proposal B · Machines: one list, grouped by what it is for","Today's Table and Tree become one view; g regroups (app, engine, network, lab) and v flips list/tree. Plain words in the side panel.")
s.term(H2,W,"viewdi — Machines",sel=[(6,2,62)],marks=[(5,0),(6,0),(5,62),(17,0),(15,62)])
s.notes([(1,"Groups are apps by default; g cycles app · engine · network · lab. ▾/▸ fold. A group shows its health in one word."),
 (2,"Every row has the same columns whatever the engine: state, kind, how to reach it, usage."),
 (3,"The side panel is written for a person: how to reach it, what it talks to, what changed, what it needs."),
 (4,"The key bar lists the verbs that exist for the selected row only (a stopped machine shows start, not stop)."),
 (5,"The Detail view's tabs (logs, inspect, network, volumes) stay, one enter deeper.")])
s.heading("Proposal C · Activity: one feed instead of Jobs, Updates and alerts","Things that need you float to the top with their action; everything else is history, newest first.")
s.term(H3,W,"viewdi — Activity",sel=[(6,2,118)],marks=[(5,0),(11,0),(13,0),(2,3)])
s.notes([(1,"NOW: alerts and running jobs, with progress and the action that fixes or cancels them."),
 (2,"Updates stop being a place you visit: they arrive in the feed, with the command shown before it runs."),
 (3,"History says who did it, including things done outside viewDI (the daemons' events)."),
 (4,"The Home count (“3 need you”) is just this feed's NOW section.")])

def r3(l,m,k): return "  "+cols([l,m,k],[40,34,6])
pal=["",
"  {B:›} rest{G:█}","",
"  {dim:ACTIONS}",
r3("{B:▶ Restart web-api}","{dim:web-stack}","{B:r}"),
r3("  Restart web-db","{dim:web-stack}","{B:r}"),
r3("  Restart redis-cache","{dim:web-stack}","{B:r}"),
"  {dim:HISTORY}",
r3("  Restarted web-api, 14:02","{dim:→ Activity}",""),
"","  {dim:also:  “pg” → Run PostgreSQL 17 · “dark” → theme · “cockpit” → open it}",
"  {dim:↑↓ choose · enter do it · tab narrows to machines, actions, library, views · esc close}",""]
def r4(l,m,k): return "  "+cols([l,m,k],[22,42,4])
act=["",
"  {B:›} web-api","",
r4("{B:▶ Open in browser}","{A::8080}","{B:b}"),
r4("  Open a shell","docker exec -it web-api sh","{B:t}"),
r4("  Logs","","{B:l}"),
r4("  Restart","","{B:r}"),
r4("  Stop","","{B:x}"),
r4("  Update the image","nginx:1.27 → nginx:1.28 available","{B:c}"),
r4("  Details","overview · inspect · network · volumes","{B:→}"),
r4("  {R:Delete…}","","{B:d}"),""]
s.heading("Proposal D · ctrl+k: type what you want instead of remembering keys","One prompt that finds machines, actions, library entries, views and settings. Every action keeps its own key; the palette teaches it.")
s.term(box("Do anything",pal,100),100,"viewdi — palette (ctrl+k)",marks=[(5,0),(7,0),(13,0)])
s.notes([(1,"Words, not keys: “rest” finds Restart for every machine, ranked by what you touched last."),
 (2,"The key of each action is on the right, so the palette is also the way to learn the keys."),
 (3,"Works from every view. “cockpit” finds the address you gave an instance with viewdi.url.")])
s.heading("Proposal E · enter opens the actions of the thing, in plain verbs","Only the verbs that make sense for that machine. A shell (docker exec / incus exec) is new: the terminal is handed over, like the engine upgrade.")
s.term(box("web-api · running",act,78),78,"viewdi — actions on web-api",marks=[(6,0),(7,0),(11,0)])
s.notes([(1,"First line is the one you most likely want (the address, if it has one)."),
 (2,"Open a shell is the new capability: no more switching to another terminal to docker exec or incus exec."),
 (3,"Update the image appears only when Updates found a newer build; Delete asks again, as today.")])
s.out(sys.argv[1])
