# Проба изоляции бокса на живом кластере

Прогон 2026-07-31 против прод-кластера Beget (контекст `83.222.27.62:26443`, k8s **v1.35.2**,
containerd 2.2.1, ядро 6.8.0, CNI cilium). Всё создавалось в одноразовом namespace
`dada-box-probe` и удалено после прогона; тейнт и метка с узла сняты.

Первый заход отвечал на два вопроса, которые `tasks/box-handoff.md` §2 относил к человеку, —
**S1** (отдельный пул узлов под боксы) и **B6** (можно ли честно обещать root внутри бокса).
Второй заход того же дня добавил то, на чём стоят манифест пода и жизненный цикл: PVC под
user namespace, допуск root-пода под PSS `restricted`, время старта на тёплом узле, in-place
resize и работающий default-deny egress. Всё ниже — наблюдения, а не рассуждения.

---

## B6 — root внутри бокса: обещание обеспечиваемо

Под с `hostUsers: false` **принят планировщиком и запустился**. Изнутри:

```
uid_map:      0 2133262336      65536
gid_map:      0 2133262336      65536
id:           uid=0(root) gid=0(root)
userns-ino:   user:[4026533694]
```

Контрольный под без `hostUsers` на том же узле:

```
uid_map:      0          0 4294967295
userns-ino:   user:[4026531837]
```

Читается так: у первого пода **свой** user namespace, и его `root` — это непривилегированный
uid `2133262336` на хосте, с диапазоном в 65536 uid. У второго — тождественное отображение на
весь диапазон, то есть хостовый root.

**Вывод:** формулировку «бокс с рутом» переписывать не нужно. Root настоящий с точки зрения
тенанта (`id` даёт `uid=0`, пакеты ставятся, mount внутри своего namespace работает) и при этом
не является хостовым root. Условие: под бокса **обязан** нести `hostUsers: false`, иначе
обещание превращается ровно в тот необеспеченный вариант, о котором предупреждал ADR-019.

Оговорка, которую нельзя терять: user namespace **не заменяет** отдельный пул узлов. Ядро у
пода общее с узлом (RuntimeClass на управляемом k8s недоступен, `kubectl get runtimeclass` —
пусто), поэтому эксплойт ядра остаётся общей поверхностью. User namespace снимает вопрос
«кто такой root внутри», а не вопрос «с кем бокс делит ядро».

## S1 — отдельный пул узлов: механизм работает, но пул решено не заказывать

Проверено на наименее нагруженном узле:

- `kubectl taint node <n> dada.io/box-pool=true:NoSchedule` и метка `dada.io/box-pool=true`
  приняты и **держатся** (перепроверено спустя 5 минут).
- Под с toleration + `nodeSelector` + `capabilities: drop ALL` + `allowPrivilegeEscalation:
  false` + `seccompProfile: RuntimeDefault` + `hostUsers: false` → **Running** на этом узле.
- Тот же под **без** toleration → **Pending**. Гейт держит.
- В кластере **нет контроллера node-group**: namespace `beget-system` пуст, CRD групп узлов
  нет. Значит выставленные руками тейнты и метки никто внутри кластера не откатывает.
- Группы узлов у Beget существуют: метка `node-group.beget.com/name`, три разные группы
  (`b2a7cd`, `f675c9`, `ff81fb`) на четырёх узлах.

**Решение владельца 2026-07-31: пул не заказываем.** Механизм проверен и остаётся доступным
рычагом — три группы узлов у Beget уже работают, тейнт держится, — но боксы будут ехать на тех
же узлах, что и платформа. Прямым текстом: чужой код исполняется на ядре, общем с control
plane, и этот остаточный риск **принят**. Что его ограничивает — перечислено ниже и всё
измерено; про ядро ни одна из мер не отвечает. Не проверено намеренно: переживут ли тейнт и
метка **пересоздание узла** группой — это понадобится, только если пул всё-таки закажут.

## Сеть: контроли есть, но дефолт дырявый

Из пода тенанта **до** политики:

| Цель | Результат |
|---|---|
| `https://example.com` | доступен |
| `https://10.96.0.1:443/version` (kube-apiserver) | **доступен, отдаёт версию** |
| IP соседнего пода | сеть доступна (`connection refused` — просто никто не слушает) |
| `http://169.254.169.254/` (метадата) | ответа нет, таймаут |

После `NetworkPolicy` default-deny egress с единственным исключением на DNS:

| Цель | Результат |
|---|---|
| `https://example.com` | **заблокирован** |
| kube-apiserver | **заблокирован** |
| сосед | **заблокирован** |

**Вывод:** cilium энфорсит `NetworkPolicy`, то есть все сетевые контроли из ФАЗА 4 доступны
средствами кластера. Но по умолчанию из пода видно и интернет, и API-сервер, и соседей —
политика для namespace боксов обязана существовать **до** первого чужого бокса, а не после.

**Мина, стоившая времени прямо в этой пробе: резолвер здесь не `kube-dns`.** Скопированное из
любого руководства правило «разрешить DNS» с селектором `namespaceSelector: kube-system` +
`k8s-app: kube-dns` не разрешает ничего, и выглядит это не как ошибка политики, а как «под
сломался»: имена не резолвятся, любой egress падает с `bad address`. В этом кластере CoreDNS
живёт в namespace **`beget-coredns`** с меткой **`k8s-app=coredns`**, ClusterIP `10.96.0.10`:

```
beget-coredns  coredns-coredns  ClusterIP  10.96.0.10  53/UDP,53/TCP  k8s-app=coredns
```

С правильным селектором DNS резолвится, а интернет остаётся закрыт — то есть нужное состояние
достижимо, и достигается оно только с этими двумя значениями.

## Как выглядит «уснуть» на подах: снимка памяти нет, но in-place resize есть

Замерено, потому что на этом стоит весь жизненный цикл (`docs/plans/2026-08-01-box-lifecycle.md`).

**Снимка памяти пода в этом кластере нет.** `kubectl get runtimeclass` пусто, gVisor нет, а
единственные апстримные пути к «заморозить и разморозить процесс» (Pod Snapshots в GKE, CRIU
checkpoint) либо требуют RuntimeClass, либо не имеют пути восстановления обратно в под. Значит
«Sleeping» не может означать «процессы стоят на паузе», и обещать это нельзя.

**Что есть вместо этого — in-place resize, GA на этом кластере (v1.35.2).** Под с
`resizePolicy: NotRequired` по cpu и memory:

| Действие | Результат |
|---|---|
| 512Mi → 1Gi (рост) | применилось, `restartCount` **0** |
| 512Mi → 384Mi (усадка выше живого RSS) | `memory.max` = `402653184`, `cpu.max` = `20000 100000`, `restartCount` **0** |
| 512Mi → 128Mi при живых 300 MiB | **отказ**, под жив, `restartCount` **0** |

Отказ выглядит так — дословно, из `.status.conditions[PodResizeInProgress]`:

```
PodResizeInProgress=True reason=Error
msg=cannot decrease memory limits: [attempting to set pod memory limit (134217728)
below current usage (319180800), attempting to set container "probe" memory limit
(134217728) below current usage (319180800)]
```

Это важнее, чем кажется: kubelet **отказывается** ужать под ниже живого потребления и говорит
почему, вместо того чтобы ужать и получить OOM-kill. То есть «додремать, ужавшись» — безопасная
операция: худший исход — отказ с внятным текстом, а не убитый бокс тенанта. Значения доезжают
до cgroup самого бокса, проверено чтением `/sys/fs/cgroup/memory.max` изнутри.

## PVC под `hostUsers: false` работает

Проверено отдельно, потому что утверждение «user namespace несовместим с PVC» встречается и
оно здесь **ложно**: Longhorn RWO PVC монтируется в под с `hostUsers: false` и `runAsUser: 0`,
запись и чтение изнутри проходят. Это снимает единственное возражение против схемы «тело
одноразовое, `/workspace` на PVC переживает перезапуск».

## PSS `restricted` и root: что именно пускает под

Namespace боксов будет под `restricted` — и там `runAsNonRoot` обязателен. Что измерено:

| Под | Вердикт |
|---|---|
| `runAsUser: 0` **без** `hostUsers` | **отклонён** admission-контролем |
| `runAsUser: 0` + `hostUsers: false` | **принят**, запустился |

То есть `hostUsers: false` — не только условие обещания про root (B6), но и ровно то, что
делает root-под допустимым под `restricted`. Один флаг закрывает оба вопроса, и убрать его из
манифеста нельзя, не потеряв обещание и не потеряв допуск одновременно.

## Время старта пода на тёплом узле

**3143 мс** от `create` до `Ready` для пода с уже вытянутым образом на узле. Это нижняя граница
для «холодного» захвата и причина, по которой парковка (`ParkingPool`) существует: продукт
обещает секунды, а не три с половиной секунды плюс инициализация userland.

---

## Как повторить

```sh
kubectl --context <ctx> apply -f - <<'YAML'
apiVersion: v1
kind: Namespace
metadata: {name: dada-box-probe}
---
apiVersion: v1
kind: Pod
metadata: {name: userns-probe, namespace: dada-box-probe}
spec:
  hostUsers: false
  restartPolicy: Never
  containers:
    - name: probe
      image: alpine:3.19
      command: ["sh", "-c", "sleep 900"]
      securityContext: {runAsUser: 0}
YAML

kubectl --context <ctx> -n dada-box-probe exec userns-probe -- \
  sh -c 'cat /proc/self/uid_map; readlink /proc/self/ns/user'

kubectl --context <ctx> delete ns dada-box-probe
```

Тейнт снимать сразу после проверки: `kubectl taint node <n> dada.io/box-pool-`.

Отказ усадки воспроизводится так — под, который **действительно** держит память (пустой
`sleep` ужмётся куда угодно и ничего не докажет; первый заход этой пробы был именно таким и был
недействителен):

```sh
kubectl -n dada-box-probe run resize-probe --image=python:3.12-alpine --restart=Never \
  --overrides='{"spec":{"hostUsers":false,"containers":[{"name":"probe","image":"python:3.12-alpine",
  "command":["python","-c","import time; b=bytearray(300*1024*1024); time.sleep(3600)"],
  "resizePolicy":[{"resourceName":"cpu","restartPolicy":"NotRequired"},
  {"resourceName":"memory","restartPolicy":"NotRequired"}],
  "resources":{"requests":{"memory":"512Mi"},"limits":{"memory":"512Mi"}}}]}}'

kubectl -n dada-box-probe patch pod resize-probe --subresource resize \
  --patch '{"spec":{"containers":[{"name":"probe","resources":{"limits":{"memory":"128Mi"},"requests":{"memory":"128Mi"}}}]}}'

kubectl -n dada-box-probe get pod resize-probe \
  -o jsonpath='{range .status.conditions[?(@.type=="PodResizeInProgress")]}{.reason} {.message}{end}'
```

Условие появляется **не мгновенно** — kubelet ставит `PodResizeInProgress=True` с пустым
`reason`, и `reason=Error` доезжает через несколько секунд. Прочитать сразу после `patch` и
сделать вывод «отказа нет» — ошибка; ждать до 30 с.

Замер времени старта: `date` в миллисекундах через `python3 -c 'import time;print(int(time.time()*1000))'`
— `date +%s%3N` под zsh падает с `bad math expression: operator expected at 'N'`.
