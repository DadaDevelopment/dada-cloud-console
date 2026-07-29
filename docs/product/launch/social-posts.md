# Посты под анонс пивота: LinkedIn и X

> Спутник `habr-article.md`. Habr несёт полный разбор, эти посты — короткие входы в него.
>
> **Перед публикацией:** ни одного числа как достижения. «Секунды» — это цель, и в постах она
> сформулирована как цель. Первый же технический читатель проверит, и лучше он проверит
> формулировку, чем обещание.

---

## LinkedIn — RU

Мы полгода строили облачную консоль и пришли к выводу, что отвечали на неправильный вопрос.

Гипотеза была такая: облака умирают, потому что достаточно дать агенту креды от VM — и он там
всё настроит сам. Первая половина верна: панель действительно умирает, потому что UI больше не
потребляет человек. Вторая половина — «поэтому продукт не нужен» — неверна.

Верная формулировка: агенты обнулили стоимость создания инфраструктуры и резко повысили
стоимость владения ею.

Из этого мы сделали ошибку: решили продавать безопасность. Обратимость действий агента,
ограниченный доступ, аудит. И посчитали сегменты.

Там, где нужна настоящая безопасность — в крупных компаниях — агента не пустят вообще, потому
что решение принимает не инженер. Там, где нужно быстро создавать с нуля, за безопасность не
заплатят: терять пока нечего. Сегмента, который одновременно несёт риск и платит именно за его
снижение, между этими полюсами нет.

Тогда мы выписали абстракции, которые действительно изменили индустрию — S3, Docker,
Kubernetes, деплой в один клик, MCP — и заметили очевидное. Ни одна не продавалась как снижение
риска. Все они делали дефицитное изобильным и все добавляли возможность. А ограничение — это
не абстракция.

Дефицит агентной эпохи оказался приземлённее, чем нам хотелось. Это не вычисления и не
безопасность. Это тело.

Агент не устаёт, не боится сломать систему и готов работать всю ночь. Ограничение — не он, а
машина, на которой он запущен. Три агента параллельно убивают ноутбук. После агента остаётся
мусор, который вы потом полгода находите. Прототип живёт на localhost, и заказчику показать
нечего.

Ни одна из этих проблем не про безопасность. Все три — про отсутствие тела.

Отдельное наблюдение, которое мне кажется самым полезным. Категория облачных сред разработки
уже существует, и у неё есть кладбище: Gitpod ушёл из категории, Codespaces остался фичей,
Coder ушёл в энтерпрайз. Причина — задержка и привязанность разработчика к своему локальному
сетапу. Посмотрите на этот список внимательно: это всё претензии человека. Агенту всё равно на
задержку, и у него нет дотфайлов.

Категория умерла для людей и переродилась для агентов.

Мы строим Dada Box: эфемерный бокс с рутом, к которому подключается ваш собственный агент;
база и S3 доцепляются на ходу; а прототип, который выжил, кристаллизуется в постоянную машину
с доменом — тем же объектом, без переезда. Одна среда проходит весь путь от одноразовой мысли
до прода, ни разу не пересоздаваясь.

Что честно не решено: время до готовности — цель в секундах, пока это цель, а не измеренный
факт, и мы будем публиковать измерения, а не обещания. Токены мы не перепродаём: вы приводите
своего агента.

Открываем приватный превью, выдаём вручную. Если вы уже гоняете агентов пачками и упирались в
свой ноутбук — напишите, вы ровно тот, с кем нам нужно поговорить.

---

## LinkedIn — EN

We spent six months building a cloud console, then concluded we had been answering the wrong
question.

Our hypothesis was that clouds are dying, because you can hand an agent credentials to a VM and
it will configure everything itself. Half of that is right: the control panel really is dying,
because a human is no longer the one consuming the UI. The other half — "therefore there's no
product" — is wrong.

The accurate version: agents drove the cost of creating infrastructure to zero, and sharply
raised the cost of owning it.

From there we made a mistake. We decided to sell safety — reversible agent actions, scoped
access instead of credentials, an audit trail of what the agent actually did. Then we did the
segment arithmetic.

Where real safety is needed — large companies — agents won't be let in at all, because the
decision isn't an engineer's to make. Where speed of creation matters, nobody pays for safety,
because there's nothing to lose yet. Between those poles there is no segment that both carries
the risk and pays specifically to reduce it.

So we listed the abstractions that actually changed the industry — S3, Docker, Kubernetes,
one-click deploy, MCP — and noticed the obvious. Not one of them was sold as risk reduction.
Every one made something scarce abundant, and every one *added* a capability. A restriction
isn't an abstraction.

The scarce thing in the agent era turned out to be more mundane than we wanted. It isn't
compute and it isn't safety. It's a body.

An agent doesn't get tired, isn't afraid of breaking the system, and will work all night. The
constraint isn't the agent — it's the machine it runs on. Three agents in parallel kill a
laptop. Agents leave behind a mess you keep finding for months. The prototype lives on
localhost, so there's nothing to show a client.

None of those problems is about safety. All three are about the absence of a body.

The observation I find most useful: the cloud dev environment category already exists and it
has a graveyard. Gitpod left the category, Codespaces stayed a feature, Coder went enterprise.
The reason was latency and developers' attachment to a local setup they'd spent years building.
Look at that list closely — those are all *human* complaints. An agent doesn't care about
latency and has no dotfiles.

The category died for humans and is reborn for agents.

We're building Dada Box: an ephemeral root box your own agent connects to, with a managed
database and object storage attaching mid-flight, and a surviving prototype crystallizing into
a permanent machine with a domain — the same object, no migration. One environment traverses
the whole path from a disposable thought to production without ever being recreated.

What's honestly unsolved: time to ready. Our target is seconds, that's still a target rather
than a measured fact, and we'll publish measurements instead of promises. We don't resell
tokens — you bring your own agent.

We're opening a private preview, granted by hand. If you already run agents in batches and have
hit the ceiling of your own laptop, get in touch — you're exactly who we need to talk to.

---

## X / Twitter — RU, тред

**1/**
Мы полгода строили облачную консоль и поняли, что отвечали на неправильный вопрос.

Гипотеза: облака умирают, потому что агенту достаточно дать креды от VM.

Панель — да, умирает. Продукт — нет. Разбор ↓

**2/**
Точная формулировка:

агенты обнулили стоимость *создания* инфраструктуры и резко подняли стоимость *владения* ею.

Создать — бесплатно. А через два месяца у вас машина, которую никто не может пересобрать.

**3/**
Из этого мы сделали ошибку: решили продавать безопасность. Обратимость, ограниченный доступ,
аудит агента.

Потом посчитали сегменты.

**4/**
Где нужна реальная безопасность — в корпорациях — агента не пустят вообще. Решает не инженер.

Где нужно быстро создавать — за безопасность не заплатят. Терять нечего.

Сегмента между этими полюсами нет.

**5/**
Тогда мы выписали абстракции, которые реально изменили индустрию:

S3 · Docker · Kubernetes · деплой в один клик · MCP

Ни одна не продавалась как снижение риска. Все делали дефицитное изобильным. Все *добавляли*
возможность.

**6/**
Ограничение — это не абстракция. Оно отнимает возможность и просит за это денег.

Правильный вопрос: что стало возможным только теперь, когда агенты существуют?

**7/**
Ответ оказался приземлённее, чем хотелось.

Дефицит — не вычисления и не безопасность. Дефицит — **тело**.

Агент не устаёт и не боится сломать систему. Ограничение — машина, на которой он запущен.

**8/**
Три вещи, которые чувствуются каждую неделю:

— третий параллельный агент убивает ноутбук
— после агента остаётся мусор, который вы находите месяцами
— прототип живёт на localhost, показать нечего

Ни одна не про безопасность.

**9/**
«Но это же Codespaces, и он не взлетел»

Справедливо. У категории есть кладбище: Gitpod ушёл, Codespaces остался фичей, Coder ушёл в
энтерпрайз.

Причина — задержка и привязанность к своему сетапу.

**10/**
Посмотрите на этот список внимательно.

Это всё претензии **человека**.

Агенту всё равно на задержку. У него нет дотфайлов, которые он полировал три года.

Категория умерла для людей и переродилась для агентов.

**11/**
Второе отличие важнее: у CDE не было выпускного пути. Бокс никогда не становился продом.

Мы строим так, что становится.

**12/**
Первая версия «кристаллизации» была мертва: записать сессию агента → модель выводит Ansible →
теневая пересборка.

Убивает одна строка:

`curl … | bash`

В записи — одна команда. В системе — сотни изменений.

**13/**
Проблема не в модели, а в уровне перехвата.

Правильный уровень — сам объект. Тогда это механический перенос, а не вывод описания. Модели в
пути нет — нечему угадать неправильно.

**14/**
Примитив, который получается:

одна среда с одним удостоверением проходит весь жизненный цикл — от трёхсекундной мысли до
прода, ни разу не пересоздаваясь.

Стадия становится свойством объекта, а не его сортом.

**15/**
Что честно не решено:

— время до готовности. Цель — секунды. Пока это цель, а не факт. Будем публиковать измерения
— перенос запущенных процессов без перезапуска. Проектируем. Не сделаем честно — перепишем
обещание, а не сделаем вид

**16/**
Токены не перепродаём и не планируем. Вы приводите своего агента и свою подписку.

Открываем приватный превью, выдаём вручную.

Если гоняете агентов пачками и упирались в ноутбук — вы тот, с кем нам надо поговорить 👇

---

## X / Twitter — EN, тред

**1/**
We spent six months building a cloud console, then realised we'd been answering the wrong
question.

Hypothesis: clouds are dying, because you can just hand an agent VM credentials.

The panel? Dying, yes. The product? No. Thread ↓

**2/**
The accurate version:

agents drove the cost of *creating* infrastructure to zero, and sharply raised the cost of
*owning* it.

Creating is free now. Two months later nobody can rebuild the machine.

**3/**
So we made a mistake: we decided to sell safety. Reversible actions, scoped access, an audit
trail of what the agent really did.

Then we did the segment arithmetic.

**4/**
Where real safety is needed — enterprises — agents won't be let in at all. An engineer doesn't
make that call.

Where creation speed matters, nobody pays for safety. Nothing to lose yet.

No segment sits between those poles.

**5/**
So we listed the abstractions that actually changed the industry:

S3 · Docker · Kubernetes · one-click deploy · MCP

Not one was sold as risk reduction. Every one made something scarce abundant. Every one *added*
a capability.

**6/**
A restriction is not an abstraction. It removes a capability and charges you for it.

The right question: what became possible only now that agents exist?

**7/**
The answer was more mundane than we wanted.

The scarce thing isn't compute and isn't safety. It's a **body**.

An agent doesn't get tired and isn't afraid of breaking things. The constraint is the machine
it runs on.

**8/**
Three things you feel weekly:

— the third parallel agent kills your laptop
— agents leave a mess you keep finding for months
— the prototype lives on localhost, nothing to show

None of them is about safety.

**9/**
"But that's Codespaces, and it didn't work."

Fair. The category has a graveyard: Gitpod left it, Codespaces stayed a feature, Coder went
enterprise.

The reason: latency, and attachment to a personal setup.

**10/**
Look at that list closely.

Those are all **human** complaints.

An agent doesn't care about latency. It has no dotfiles it spent three years polishing.

The category died for humans and is reborn for agents.

**11/**
The bigger difference: CDEs had no graduation path. The box never became production.

We're building it so that it does.

**12/**
Our first "crystallization" design was dead on arrival: record the agent session → a model
infers Ansible → shadow rebuild verifies it.

One line kills it:

`curl … | bash`

One command recorded. Hundreds of changes made.

**13/**
The problem isn't the model, it's the interception layer.

The right layer is the object itself. Then it's a mechanical move, not an inferred description.
No model in the path — nothing to guess wrong.

**14/**
The resulting primitive:

one environment, one identity, traversing the entire lifecycle — from a three-second disposable
thought to production — without ever being recreated.

Lifecycle stage becomes a property of the object, not a species of object.

**15/**
Honestly unsolved:

— time to ready. Target is seconds. Still a target, not a fact. We'll publish measurements
— moving running processes without a restart. In design. If it can't be done honestly, we
rewrite the promise rather than fake it

**16/**
We don't resell tokens and don't plan to. You bring your own agent and your own subscription.

Private preview, granted by hand.

If you already run agents in batches and have hit your laptop's ceiling — you're who we need
to talk to 👇

---

## Заметки по публикации

- **Порядок:** сначала Habr, потом посты со ссылкой на него. Habr несёт доказательную часть,
  посты без неё выглядят как заявление.
- **Самая сильная мысль для обсуждения** — «категория умерла для людей и переродилась для
  агентов». Её стоит вынести в первый комментарий под постом, если алгоритм режет длинные
  тексты.
- **Ожидаемое возражение №1:** «это Codespaces / e2b / Daytona». Отвечать выпускным путём, а не
  спором про песочницы: бокс, который становится продом, — это то, чего у них нет.
- **Ожидаемое возражение №2:** «возьму VPS за 300 ₽». Отвечать параллелизмом и тарификацией за
  активные минуты, а не удалённостью как ценностью.
- **Не спорить** в комментариях про то, умирают ли облака. Тезис статьи не про это, а увести в
  этот спор легко.
