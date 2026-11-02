import re, html, sys
# Everforest Dark
P=dict(bg="#2d353b",bg2="#343f44",sel="#475258",fg="#d3c6aa",dim="#859289",G="#a7c080",A="#83c092",B="#7fbbb3",Y="#dbbc7f",O="#e69875",R="#e67e80",V="#d699b6")
CW,LH=8.4,18
TOK=re.compile(r'\{(\w+):((?:[^{}])*)\}')
def vis(s): return len(TOK.sub(lambda m:m.group(2),s))
def pad(s,w):
    v=vis(s); assert v<=w,(v,w,s); return s+" "*(w-v)
def segs(s):
    out=[];i=0
    for m in TOK.finditer(s):
        if m.start()>i: out.append(("fg",s[i:m.start()]))
        out.append((m.group(1),m.group(2))); i=m.end()
    if i<len(s): out.append(("fg",s[i:]))
    return out
def box(title,rows,w,c="dim"):
    inner=w-2
    t=("─ "+title+" ") if title else ""
    top="{%s:╭%s%s╮}"%(c,"","") 
    lines=["{%s:╭}"%c+("{%s:─ }{B:%s}{%s: }"%(c,title,c) if title else "")+"{%s:%s╮}"%(c,"─"*(inner-(len(title)+3 if title else 0)))]
    for r in rows: lines.append("{%s:│}"%c+pad(r,inner)+"{%s:│}"%c)
    lines.append("{%s:╰%s╯}"%(c,"─"*inner))
    return lines
def cols(parts,widths):
    return "".join(pad(p,w) for p,w in zip(parts,widths))

class SVG:
    def __init__(s,width): s.w=width; s.el=[]; s.y=20
    def text(s,x,y,t,fill,size=14,weight="normal",family="sans"):
        fam="DejaVu Sans, Helvetica, Arial, sans-serif" if family=="sans" else "DejaVu Sans Mono, Menlo, Consolas, monospace"
        s.el.append(f'<text x="{x:.1f}" y="{y:.1f}" fill="{fill}" font-size="{size}" font-weight="{weight}" font-family="{fam}">{html.escape(t)}</text>')
    def term(s,lines,cols_,title,sel=None,marks=()):
        x0=20; pad_=14; tw=cols_*CW+2*pad_; th=len(lines)*LH+2*pad_+26
        y0=s.y
        s.el.append(f'<rect x="{x0}" y="{y0}" width="{tw}" height="{th}" rx="10" fill="{P["bg"]}" stroke="#1e2326"/>')
        s.el.append(f'<rect x="{x0}" y="{y0}" width="{tw}" height="26" rx="10" fill="#232a2e"/><rect x="{x0}" y="{y0+16}" width="{tw}" height="10" fill="#232a2e"/>')
        for i,c in enumerate(["#e67e80","#dbbc7f","#a7c080"]):
            s.el.append(f'<circle cx="{x0+16+i*18}" cy="{y0+13}" r="5.5" fill="{c}"/>')
        s.text(x0+tw/2-len(title)*3.6,y0+18,title,P["dim"],13)
        ty=y0+26+pad_
        for (row,c0,c1) in (sel or []):
            s.el.append(f'<rect x="{x0+pad_+c0*CW:.1f}" y="{ty+row*LH:.1f}" width="{(c1-c0)*CW:.1f}" height="{LH}" fill="{P["sel"]}"/>')
        for i,l in enumerate(lines):
            col=0
            parts=[]
            for c,t in segs(l):
                if t.strip():
                    bold = c.isupper() and c in "GAYBORV" and False
                    fill=P.get(c,P["fg"])
                    w="bold" if c in("B",) else "normal"
                    parts.append(f'<tspan x="{x0+pad_+col*CW:.1f}" fill="{fill}" font-weight="{w}">{html.escape(t)}</tspan>')
                col+=len(t)
            s.el.append(f'<text y="{ty+i*LH+13:.1f}" font-size="14" xml:space="preserve" font-family="DejaVu Sans Mono, Menlo, Consolas, monospace">{"".join(parts)}</text>')
        for n,(row,col) in enumerate(marks,1):
            cx=x0+pad_+col*CW; cy=ty+row*LH+9
            s.el.append(f'<circle cx="{cx:.1f}" cy="{cy:.1f}" r="10" fill="{P["O"]}"/>')
            s.text(cx-4.3,cy+5,str(n),"#232a2e",13,"bold")
        s.y=y0+th+16
        return tw
    def heading(s,t,sub=None):
        s.y+=10; s.text(20,s.y+8,t,"#2d353b",19,"bold"); s.y+=18
        if sub: s.text(20,s.y+8,sub,"#5c6a72",13.5); s.y+=18
        s.y+=8
    def notes(s,items):
        for n,t in items:
            if n: 
                s.el.append(f'<circle cx="30" cy="{s.y+4}" r="9" fill="{P["O"]}"/>'); s.text(26,s.y+9,str(n),"#232a2e",12,"bold")
            s.text(46,s.y+9,t,"#3a454a",13.5); s.y+=22
        s.y+=10
    def out(s,path):
        h=s.y+10
        body="\n".join(s.el)
        open(path,"w").write(f'<svg xmlns="http://www.w3.org/2000/svg" width="{s.w}" height="{h:.0f}" viewBox="0 0 {s.w} {h:.0f}">\n<rect width="100%" height="100%" fill="#fdf6e3"/>\n{body}\n</svg>\n')
