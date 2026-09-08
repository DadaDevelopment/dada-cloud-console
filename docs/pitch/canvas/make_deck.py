import re,json,io
order=json.load(open("canvas.json"))["artboards"]
css=None; slides=[]
for a in order:
    src=open(a["file"],encoding="utf-8").read()
    st=re.search(r"<style>(.*?)</style>",src,re.S).group(1)
    if css is None: css=re.sub(r"\.slide\{background:[^}]*\}","",st)
    bg=re.search(r"\.slide\{background:(.*?)\}",st,re.S).group(1).strip()
    body=re.search(r'(<div class="slide">.*</div>)\s*</x-dc>',src,re.S).group(1)
    slides.append(body.replace('<div class="slide">','<div class="slide" style="background:%s">'%bg,1))
html="""<!doctype html><html lang="ru"><head><meta charset="utf-8"><title>Dada Cloud</title><style>
%s
html,body{background:#0b1220}
@page{size:1600px 900px;margin:0}
.slide{page-break-after:always;break-after:page}
.slide:last-child{page-break-after:auto}
</style></head><body>
%s
</body></html>""" % (css,"\n".join(slides))
open("deck.html","w",encoding="utf-8").write(html)
print("deck.html",len(slides))
