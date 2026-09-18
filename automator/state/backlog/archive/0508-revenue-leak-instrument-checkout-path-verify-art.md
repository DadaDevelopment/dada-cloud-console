---
id: 0508
status: closed
prio: P1
stream: 2
hypothesis: H03
title: Revenue leak: instrument checkout path, verify artem returns after undefined-link fix (H03)
created: 2026-09-17
sess: sess-0917a
closed_at: 2026-09-18
closed_commit: 2171f58c
closed_note: sess-0918a: checkout path instrumented and SHIPPED to prod (Jenkins #105 on 565f1641, pod runs the image). Markers: billing_checkout:click:<plan> / redirect / 3 error variants + data-ux on both buttons + Metrika goal checkout_redirect created live as id 637214589 in the same cycle. artem verified live: 3 sessions after 7f59ca7f (09-14/15/17), each saw recovery.apps.view, 0 clicks, 0 dismiss, never reached billing page - he is not lost to the CTA, he never navigated there; the wall (09-25) is his next forced contact, now fully measured.
---
