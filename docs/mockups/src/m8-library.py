import os; exec(open(os.path.join(os.path.dirname(__file__), "svgmock.py")).read())
W=120
def header(active):
    tabs=["1 Admin","2 Resources","3 Table","4 Tree","5 Jobs","6 Updates","7 Library"]
    t="  "+"  ".join(("{B:[%s]}"%x if x.startswith(active) else "{dim:%s}"%x) for x in tabs)
    return [pad(" {G:viewDI}",W-37)+"{G:●} Docker 29.0.1  {G:●} Incus 6.0.5  {Y:⬆ 5}",
            pad(t,W-23)+"{dim:theme: Everforest Dark}"]
cat=["{dim: CATEGORY}","",
"   All           38","   Databases      5","   Web & proxies  4","   Monitoring     3",
"   Messaging      2","   Self-hosted    9","   Dev tools      3","   Base images    4",
" {B:▸ Linux systems}  8","   Mine           1","",
" {dim:ENGINE  ◂ both ▸}"]
lst=["{dim:  NAME                IMAGE             KIND   STATE}","",
"▶ Debian 12 + Docker  {A:debian/12/cloud}   {V:ctr vm} {Y:⤓ local}",
"  Debian 12           {A:debian/12}         {V:ctr vm} {G:● 2 up}",
"  Ubuntu 24.04 LTS    {A:ubuntu/24.04}      {V:ctr vm}",
"  Alpine 3.21         {A:alpine/3.21}       {V:ctr}",
"  Arch Linux          {A:archlinux}         {V:ctr vm}",
"  Fedora 41           {A:fedora/41}         {V:ctr vm}",
"  Rocky Linux 9       {A:rockylinux/9}      {V:ctr vm} {Y:⤓ local}",
"  Ubuntu + k3s        {A:ubuntu/24.04/cloud}{V:    vm}"]
det=["{B:Debian 12 + Docker}      {dim:incus · built-in}","Debian with Docker set up on first boot.","",
"{dim:Image}    {A:images:debian/12/cloud}",
"{dim:Local}    {Y:⤓ cached 2 d ago} (no download)",
"{dim:Kind}     container (vm also works)",
"{dim:Limits}   2 CPU · 2 GiB",
"{dim:Profile}  default",
"{dim:Init}     cloud-init: installs docker.io,",
"         adds the default user to docker",
"{dim:Docs}     linuxcontainers.org/incus",
"",
"{dim:After}    incus exec <name> -- docker ps",
"{dim:Running}  {dim:none yet}"]
rows=[" {dim:Docker 29.0.1 · Incus 6.0.5 · 30 Docker + 8 Incus entries, 1 yours · browsing never touches the network}",""]
for i in range(max(len(cat),len(lst),len(det))):
    g=lambda L:L[i] if i<len(L) else ""
    rows.append(cols([g(cat),"{dim:│} "+g(lst),"{dim:│} "+g(det)],[20,56,42]))
rows+=["",pad(" {B:enter} run  {B:/} filter  {B:←→} category  {B:e} engine  {B:S} search Docker Hub  {B:v} reveal  {B:o} sort",118)]
L1=header("7")+box("Library · Linux systems",rows,W)

s=SVG(1060)
s.heading("viewDI · Stage 8 draft · Library view (7)","Container library for v1.1.0 — 120×40, Everforest Dark. Not built yet: a mockup for validation.")
s.term(L1,W,"viewdi — 120×40",sel=[(7,21,77)],marks=[(6,4),(6,45),(6,63),(4,99),(21,103)])
s.notes([(1,"Categories, with an entry count. “Linux systems” holds the Incus images; “Mine” your own entries from ~/.config/viewDI/library/."),
 (2,"Image: Incus aliases on the images: remote; Docker references with a pinned major tag (postgres:17)."),
 (3,"KIND (Incus): container, VM or both. STATE: ⤓ already downloaded · ● running instances created from this entry."),
 (4,"Panel of the selected entry: what will be created. <name> is filled in by the form; secrets stay masked."),
 (5,"Keys. e cycles the engine filter (both / Docker / Incus); enter opens the prefilled create form.")])

f1=["","  {B:Run Debian 12 + Docker from the library}","  {dim:on Incus 6.0.5 · project default · progress shows in Jobs (5)}","",
"  Image        {A:images:debian/12/cloud}    {Y:⤓ already local}",
"  Name         debian-docker-2           {O:debian-docker is taken}",
"  Type         ◂ {B:container} ▸",
"  Profiles     default",
"  CPU          2",
"  Memory       2GiB",
"  Pool         {dim:the profile's (default)}",
"  Cloud-init   {dim:#cloud-config · 7 lines}   {B:enter} views and edits",
"  Labels       user.viewdi.library=debian-docker",
"  Start        [x] start the instance once created","",
"                 {dim:Cancel}    {B:[ Create ]}",""]
s.heading("Run an Incus entry","enter opens the Stage 4b Incus form, prefilled and editable; viewDI picks a free name and says why.")
s.term(box("",f1,78),78,"create form · Incus")

f2=["","  {B:Run PostgreSQL 17 from the library}","  {dim:on Docker 29.0.1 · the job's progress shows in Jobs (5)}","",
"  Image        {A:postgres:17}                 {Y:⤓ already local}",
"  Pull         [ ] pull the image first",
"  Name         postgres-2                  {O:postgres is taken}",
"  Ports        5433:5432                   {O:5432 is published by postgres}",
"  Environment  POSTGRES_PASSWORD={dim:●●●●●●●●●●●●}  {dim:(generated, v reveals)}",
"               POSTGRES_USER=postgres  POSTGRES_DB=app",
"  Volumes      postgres-2-data:/var/lib/postgresql/data",
"  Restart      ◂ {B:unless-stopped} ▸",
"  Labels       io.viewdi.library=postgres",
"  Start        [x] start the container once created","",
"                 {dim:Cancel}    {B:[ Create ]}",""]
s.heading("Run a Docker entry","Same flow for Docker: name and host port move to free ones, secrets are generated (crypto/rand) and masked.")
s.term(box("",f2,78),78,"create form · Docker")

j=["{dim: STATE  KIND    TARGET           ENGINE  PROGRESS                         TIME}",
" {G:✓ done} create  debian-docker-2  incus   created · started · cloud-init ok   38 s",
"        After: incus exec debian-docker-2 -- docker ps",
" {G:✓ done} create  postgres-2       docker  created · started                    4 s",
"        After: psql -h localhost -p 5433 -U postgres",
"        {dim:the password is in the container's environment (Overview, v reveals)}"]
s.heading("When the job ends","Jobs (5) shows the entry's note with the real name and port. The password is never repeated there.")
s.term(box("Jobs · 2 done",j,90),90,"Jobs")
s.out(sys.argv[1])
