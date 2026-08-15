# UI surface brief

Primary user and task: a self-hosting operator scanning monitor health and
opening one monitor without losing the inventory context.

Usage scene and constraints: networked browser against an external Xisnove BFF;
hybrid homelab; useful at 390 px and 1440 px; keyboard-first operation; the BFF
uses only the public SDK and pages are authoritative, `no-store` reads.

Product register: product. Archetype: App Shell + Operations List + Detail
Workspace. Information priority: degraded/unknown health, monitor identity,
current health, configuration, then unavailable history. Navigation model:
native links with HTMX fragments and canonical URLs. Consequential states:
loading, empty, filtered-empty, partial/unknown, upstream failure, sign-in
failure, signed-out recovery, and unknown route. Existing identity: `X-9`
remains the working product shorthand, while the shell uses the canonical
Xisnove V10 logo, independent mark, reverse mark, and favicon from
`araihu/assets@ab01f1a0f592e4f1398173df04e4f8fc013cb21a`. The assets are served
from immutable same-origin routes; only the Arai Hû organization identity uses
the cloud motif, while Xisnove keeps its independent product symbol.

Density: compact for the inventory and standard for detail. Motion: restrained;
only Goshtoso loading/drawer transitions explain state. Visual direction: one
dense inventory, semantic health and selected-state tokens, restrained dividers,
one subordinate metadata rail, and no decorative elevation. Primitives:
`AppShell`, `PageHeader`, `Toolbar`, `Table`, `Badge`, `Alert`, `EmptyState`,
`Skeleton`, `Button`, `Link`, `Drawer`, and `Sidebar.Overlay`. The state/action invariant
ledger is [state-action-ledger.md](state-action-ledger.md).

No public primitive matches the monitor definition-list/detail composition, so
that small responsive workspace remains application-owned CSS over Goshtoso
semantic custom properties.
