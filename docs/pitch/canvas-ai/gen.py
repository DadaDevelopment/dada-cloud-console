import json

CSS = open("_css.txt", encoding="utf-8").read() + """
.tsam{display:flex;align-items:center;gap:40px;margin-top:4px}
.rings{position:relative;width:420px;height:420px;flex:0 0 420px}
.ring{position:absolute;border-radius:50%;border:2px solid rgba(255,255,255,.45);display:flex;align-items:flex-start;justify-content:center;padding-top:14px;font-size:22px;font-weight:800}
.r1{width:420px;height:420px;left:0;top:0;background:rgba(255,255,255,.07)}
.r2{width:290px;height:290px;left:65px;top:110px;background:rgba(255,255,255,.13)}
.r3{width:165px;height:165px;left:127px;top:225px;background:rgba(56,189,248,.35);border-color:rgba(56,189,248,.7)}
.mkey{flex:1 1 auto;display:flex;flex-direction:column;gap:18px}
.mrow{border-left:4px solid rgba(255,255,255,.35);padding-left:20px}
.mrow b{display:block;font-size:34px;font-weight:800;letter-spacing:-.6px}
.mrow span{font-size:21px;opacity:.8;line-height:1.35;display:block;margin-top:6px}
.mrow.sel{border-left-color:#38bdf8}
.tl{display:flex;gap:0;margin-top:30px}
.tlc{flex:1 1 0;position:relative;padding-top:44px}
.tlc:before{content:"";position:absolute;left:0;right:0;top:16px;height:3px;background:rgba(255,255,255,.28)}
.tlc:after{content:"";position:absolute;left:0;top:8px;width:20px;height:20px;border-radius:50%;background:#38bdf8}
.tlc.past:after{background:rgba(255,255,255,.6)}
.tlc h4{font-size:26px;font-weight:800;margin-bottom:8px}
.tlc p{font-size:20px;line-height:1.35;opacity:.82;padding-right:22px}
.ctab{width:100%;border-collapse:collapse;margin-top:10px}
.ctab th{text-align:left;font-size:21px;font-weight:800;padding:14px 16px;opacity:.75;border-bottom:2px solid rgba(255,255,255,.3)}
.ctab td{font-size:22px;padding:16px;border-bottom:1px solid rgba(255,255,255,.16);line-height:1.3}
.ctab td.k{font-weight:800;width:24%}
.ctab .us{background:rgba(56,189,248,.16)}
.ctab th.us{background:rgba(56,189,248,.16);opacity:1}
.src{font-size:16px;opacity:.5;margin-top:14px;line-height:1.4}
"""

LOGO = '<span class="logo"><svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/></svg></span>'

BG = {
  "blue": "linear-gradient(160deg,#1e3a8a 0%,#2563eb 100%)",
  "indigo": "linear-gradient(160deg,#312e81 0%,#4f46e5 100%)",
  "teal": "linear-gradient(160deg,#0f766e 0%,#0891b2 100%)",
  "slate": "linear-gradient(160deg,#0b1220 0%,#1e293b 100%)",
}


def slide(name, bg, num, body):
    head = '<div class="brandrow">%s<span class="brandname">DADA Cloud</span><span class="num">%s</span></div>' % (LOGO, num)
    foot = '<div class="foot"><span>Академия инноваторов · 10 поток</span><span>dada-tuda.ru</span></div>'
    html = """<script src="./support.js"></script>
<x-dc>
<helmet>
<style>
%s
.slide{background:%s}
</style>
</helmet>
<div class="slide">
%s
%s
%s
</div>
</x-dc>
""" % (CSS, BG[bg], head, body, foot)
    open(name, "w", encoding="utf-8").write(html)


def frame(cap, t, shot):
    return ('<div><div class="frame" style="height:212px"><div class="bar"><span class="dot" style="background:#ff5f57"></span>'
            '<span class="dot" style="background:#febc2e"></span><span class="dot" style="background:#28c840"></span></div>'
            '<img src="%s" alt=""></div><div class="fcap">%s</div><div class="ftime">%s</div></div>') % (shot, cap, t)


slide("Main.dc.html", "blue", "01", """
<div class="content center" style="display:flex;flex-direction:column">
  <div class="title big">Облако, где приложение<br>запускает автор,<br>а не инженер.</div>
  <div class="subtitle" style="margin-top:30px;font-size:30px">dada-tuda.ru — работающий продукт, а не презентация идеи</div>
  <div class="pills" style="justify-content:center;margin-top:36px">
    <span class="pill">Марков-Бутырский Алекс Андреевич, основатель</span>
    <span class="pill">@java_er</span>
    <span class="pill">+7 931 305-11-30</span>
  </div>
</div>""")

slide("S02-Problema.dc.html", "indigo", "02", """
<div class="kicker">Проблема</div>
<div class="title">Написать программу — вечер.<br>Запустить её в интернет — неделя.</div>
<div class="content cols" style="margin-top:26px">
  <div class="card"><div class="lead">Написать</div><h3>Час. Один человек.</h3><p>Помощник-ИИ пишет код за автора. Цена написания упала почти до нуля.</p></div>
  <div class="card solid"><div class="lead">Запустить</div><h3>Неделя. Отдельный специалист.</h3><p>Настройка, адрес, база, защищённое соединение, поддержка. Счёт на десятки тысяч в месяц.</p></div>
</div>
<div class="content" style="margin-top:22px">
  <div class="funnel">
    <div class="fbar" style="width:100%;height:60px"><span>Зашли на сайт</span><span class="v">361</span></div>
    <div class="fbar" style="width:70%;height:60px"><span>Зарегистрировались</span><span class="v">12 из 100</span></div>
    <div class="fbar" style="width:52%;height:60px;background:rgba(56,189,248,.28);border-color:rgba(56,189,248,.5)"><span>Дошли до приложения</span><span class="v">каждый третий</span></div>
  </div>
  <div class="note">Мы считаем эти числа на своих пользователях каждую неделю. Срез 30 дней, 1 сентября 2026.</div>
</div>""")

slide("S03-Reshenie.dc.html", "blue", "03", """
<div class="kicker">Решение</div>
<div class="title">Три шага вместо недели настройки</div>
<div class="content" style="margin-top:22px">
  <ul class="list" style="margin-top:0;font-size:.94em">
    <li><span class="n">1</span><div><b>Принесли код</b> — папкой, архивом или ссылкой на репозиторий.</div></li>
    <li><span class="n">2</span><div><b>Получили живой адрес</b> в интернете. Минуты, не дни.</div></li>
    <li><span class="n">3</span><div><b>Дальше платформа держит его живым сама:</b> база данных, домен, защищённое соединение, откат на прошлую версию.</div></li>
  </ul>
  <div class="frames" style="margin-top:18px">%s%s%s%s</div>
  <div class="note">Кадры сняты с экрана во время реального прогона на продукте.</div>
</div>""" % (
    frame("Принесли код", "0:00", "shot1.jpg"),
    frame("Идёт сборка", "0:40", "shot3.jpg"),
    frame("Собралось", "2:10", "shot2.jpg"),
    frame("Живой адрес", "2:15", "shot4.jpg"),
))

slide("S04-Tehnologiya.dc.html", "slate", "04", """
<div class="kicker">Технология</div>
<div class="title">Внутри работает агент,<br>который не советует, а чинит</div>
<div class="content cols3" style="margin-top:26px">
  <div class="card"><div class="lead">Самопочинка</div><h3>Приложение упало — агент разобрался</h3><p>Он смотрит журнал, находит причину и применяет исправление или предлагает его. Не подсказка в чате, а действие.</p></div>
  <div class="card"><div class="lead">Среда для агентов</div><h3>Одноразовое рабочее место за секунды</h3><p>В нём агент может ломать что угодно. Прототип выжил — становится настоящим сервисом без переезда.</p></div>
  <div class="card"><div class="lead">Владение</div><h3>Понятный счёт и внятная причина сбоя</h3><p>Ошиблись — откат на прошлую версию. Стало тихо — платформа сообщит сама, а не клиент нам.</p></div>
</div>
<div class="note">Наше преимущество — доступ к тому, что происходит с приложением клиента. Чужой помощник этого не видит и починить не может.</div>""")

slide("S05-Rynok.dc.html", "teal", "05", """
<div class="kicker">Объём рынка</div>
<div class="title">Растущий рынок,<br>в котором нашего класса решений нет</div>
<div class="content tsam">
  <div class="rings">
    <div class="ring r1">TAM</div>
    <div class="ring r2">SAM</div>
    <div class="ring r3">SOM</div>
  </div>
  <div class="mkey">
    <div class="mrow"><b>235 млрд ₽ / год — TAM</b><span>Российский рынок аренды вычислений и платформ для размещения приложений, 2025 год. Рост за год — 40%.</span></div>
    <div class="mrow"><b>96 млрд ₽ / год — SAM</b><span>Публичные инфраструктурные сервисы самообслуживания: те, что берут картой и без договора.</span></div>
    <div class="mrow sel"><b>≈1 млрд ₽ / год — SOM</b><span>Наша цель на горизонте трёх лет: около 1% SAM. Это порядка 10 000 платящих команд по 8 000 ₽ в месяц.</span></div>
  </div>
</div>
<div class="src">Источники: TAdviser «PaaS (рынок России)», iKS-Consulting «Российский рынок облачных инфраструктурных сервисов 2025». SOM — наша оценка, не отраслевая цифра.</div>""")

slide("S06-Konkurenty.dc.html", "indigo", "06", """
<div class="kicker">Конкуренты</div>
<div class="title">Создать сервер умеет каждый.<br>Отвечать за то, что дальше, — никто.</div>
<div class="content">
  <table class="ctab">
    <tr><th></th><th>Классические хостинги</th><th>Большие облака</th><th class="us">DADA Cloud</th></tr>
    <tr><td class="k">Кому продают</td><td>всем подряд</td><td>инженерным службам</td><td class="us">авторам и командам без инженеров</td></tr>
    <tr><td class="k">Что отдают</td><td>сервер, дальше сами</td><td>конструктор из сотни кнопок</td><td class="us">работающий адрес в интернете</td></tr>
    <tr><td class="k">Порог входа</td><td>нужен свой специалист</td><td>нужна своя команда</td><td class="us">нужен только код</td></tr>
    <tr><td class="k">Когда сломалось</td><td>пишите в поддержку</td><td>разбирайтесь сами</td><td class="us">агент чинит или называет причину</td></tr>
    <tr><td class="k">Счёт</td><td>тариф-коробка</td><td>непредсказуемый</td><td class="us">поминутно и только за живое</td></tr>
  </table>
  <div class="note">Отдельные компании не называем: разница проходит по классу решения, а не по конкретному имени.</div>
</div>""")

slide("S07-Biznes-model.dc.html", "blue", "07", """
<div class="kicker">Бизнес-модель</div>
<div class="title">Счёт идёт поминутно<br>и только за то, что работает</div>
<div class="content cols3" style="margin-top:24px">
  <div class="card"><h3>Ничего не запущено</h3><p>Счёт не растёт. Совсем.</p></div>
  <div class="card"><h3>Бесплатный уровень</h3><p>Одно приложение. Засыпает, когда нет посетителей.</p></div>
  <div class="card"><h3>Дальше — по факту</h3><p>Память, вычисления и диск по факту. Минимальный счёт засчитывается в потребление, а не сверху.</p></div>
</div>
<div class="content cols" style="margin-top:24px">
  <div class="card"><h3>Расходы почти постоянны</h3><p>Держать платформу стоит фиксированную сумму в месяц, и она почти не растёт от новых клиентов. Каждый следующий клиент идёт в маржу.</p></div>
  <div class="card"><h3>Мощности заняты на 15%</h3><p>Свободного ресурса хватает на порядок больше пользователей, чем есть сегодня. Точка безубыточности — десятки платящих клиентов, а не тысячи.</p></div>
</div>
<div class="note">Приём платежей подключён и проверен целиком. Тарифов-коробок, в которые никто не попадает, не делаем.</div>""")

slide("S08-Dinamika.dc.html", "slate", "08", """
<div class="kicker">Динамика развития</div>
<div class="title">Работающий продукт, а не прототип</div>
<div class="content">
  <div class="hero-metrics">
    <div class="card"><div class="metric">47<small>зарегистрированных пользователей</small></div></div>
    <div class="card"><div class="metric">74<small>проекта</small></div></div>
    <div class="card"><div class="metric">109<small>приложений создано</small></div></div>
    <div class="card"><div class="metric">82<small>живут прямо сейчас</small></div></div>
  </div>
  <div class="tl">
    <div class="tlc past"><h4>2025</h4><p>Начали платформу. Первый запуск чужого приложения.</p></div>
    <div class="tlc past"><h4>Начало 2026</h4><p>Открыли вход, появились первые пользователи со стороны.</p></div>
    <div class="tlc"><h4>Сегодня</h4><p>82 живых приложения, 113 успешных запусков за неделю, приём платежей включён.</p></div>
    <div class="tlc"><h4>Через 6 месяцев</h4><p>Первые платящие клиенты и повторные оплаты, первые пилоты со студиями.</p></div>
    <div class="tlc"><h4>Через 3 года</h4><p>Около 1% адресуемого рынка: порядка 10 000 платящих команд.</p></div>
  </div>
  <div class="note">Данные на 1 сентября 2026. На платформе живут чужие приложения, которые пережили не один наш сбой и восстановление.</div>
</div>""")

slide("S09-Komanda.dc.html", "indigo", "09", """
<div class="kicker">Команда</div>
<div class="title">Один основатель<br>и команда ИИ-агентов</div>
<div class="content">
  <div class="team">
    <div class="hub">Марков-Бутырский<br>Алекс<br>основатель</div>
    <div class="nodes">
      <div class="node">Разработка<small>агенты пишут и выкатывают код</small></div>
      <div class="node">Дежурство<small>круглосуточно, в моё отсутствие</small></div>
      <div class="node">Разбор сбоев<small>причина и письменный след</small></div>
      <div class="node">Аналитика и тексты<small>метрики, лендинги, письма</small></div>
    </div>
  </div>
  <div class="bigline" style="margin-top:24px">Платформа на 100+ приложений держится без штата инженеров:<br>мы сами живём внутри того, что продаём.</div>
  <div class="note">Сильная сторона — инженерная. Слабая — продажи и выход на заказчиков. Именно её закрываем в программе.</div>
</div>""")


def goal(txt, now, tgt):
    return '<div class="goal"><span class="txt">%s</span><span class="now">%s</span><span class="arrow">→</span><span class="tgt">%s</span></div>' % (txt, now, tgt)


slide("S10-Zapros.dc.html", "teal", "10", """
<div class="kicker">Запрос</div>
<div class="title">Строить умеем. Продавать пока нет.</div>
<div class="content cols3" style="margin-top:22px">
  <div class="card"><h3>Доступ к заказчикам</h3><p>Студии, продуктовые команды, корпоративные подразделения. У нас нет к ним двери, у программы она есть.</p></div>
  <div class="card"><h3>Трекер по деньгам</h3><p>Продукт есть, выручки нет. Нужен человек, который снимет с нас инженерную оптику и спросит про деньги.</p></div>
  <div class="card"><h3>Первые пилоты и партнёры</h3><p>Дистрибуция через тех, у кого уже есть аудитория авторов.</p></div>
</div>
<div class="content" style="margin-top:20px">
%s%s%s
  <div class="pills" style="margin-top:20px"><span class="pill">lexagri200430@gmail.com</span><span class="pill">+7 931 305-11-30</span><span class="pill">@java_er</span><span class="pill">dada-tuda.ru</span></div>
</div>""" % (
    goal("Зарегистрировался → работающее приложение", "каждый третий", "половина"),
    goal("Платящие клиенты и повторные оплаты", "0", "первые"),
    goal("Корпоративные пилоты: студии и продуктовые команды", "0", "первые"),
))

order = ["Main.dc.html", "S02-Problema.dc.html", "S03-Reshenie.dc.html", "S04-Tehnologiya.dc.html",
         "S05-Rynok.dc.html", "S06-Konkurenty.dc.html", "S07-Biznes-model.dc.html", "S08-Dinamika.dc.html",
         "S09-Komanda.dc.html", "S10-Zapros.dc.html"]

W, H = 1600, 900
GX, GY = W + 120, H + 180
boards = []
for i, f in enumerate(order):
    boards.append({"file": f, "x": (i % 4) * GX, "y": (i // 4) * GY, "w": W, "h": H, "print": "fixed"})
json.dump({"artboards": boards, "launch": {"view": "canvas"}}, open("canvas.json", "w"), ensure_ascii=False, indent=2)
print("wrote", len(order), "artboards")
