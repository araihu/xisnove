---
name: X-9
description: An external monitoring control plane for mixed homelab infrastructure.
colors:
  signal: "var(--color-primary)"
  surface: "var(--color-surface)"
  surface-alt: "var(--color-surface-alt)"
  ink: "var(--color-on-surface-strong)"
  ink-muted: "var(--color-on-surface-muted)"
  outline: "var(--color-outline)"
  success: "var(--color-success-text)"
  warning: "var(--color-warning-text)"
  danger: "var(--color-danger-text)"
typography:
  headline:
    fontFamily: "var(--font-sans, ui-sans-serif, system-ui, sans-serif)"
    fontWeight: 700
    lineHeight: 1.15
  body:
    fontFamily: "var(--font-sans, ui-sans-serif, system-ui, sans-serif)"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "var(--font-sans, ui-sans-serif, system-ui, sans-serif)"
    fontWeight: 600
    lineHeight: 1.25
rounded:
  control: "var(--radius-md, 0.5rem)"
  surface: "var(--radius-lg, 0.75rem)"
spacing:
  xs: "0.25rem"
  sm: "0.5rem"
  md: "1rem"
  lg: "1.5rem"
  xl: "2rem"
---

# Design System: X-9

## Overview

<!-- impeccable:direction-seed 19633b70 -->

**Creative North Star: “Signal Box Dispatch”**

X-9 feels like a calm external dispatch room: compact, legible, and alert to changes without looking permanently alarmed. Railway signal-box logic informs hierarchy—routes, posts, blocks, and evidence freshness—but never becomes costume or decoration. The interface is an operational instrument built from Goshtoso primitives and semantic theme tokens.

The first viewport answers three questions in order: what needs attention, what infrastructure is being observed, and what safe action comes next. Density is deliberate. Structure comes from alignment, rules, state labels, and one selected route rather than a wall of floating cards.

**Key Characteristics:**

- Persistent application shell with one primary scroll region.
- Compact global command search in the top bar.
- Collection-first workspaces with explicit freshness and partial-state handling.
- Right-side drawers for inspection without losing list context.
- Semantic status color reinforced by text and shape, never color alone.

## Colors

The organization-owned Arai Hû theme is the default palette. It overrides Goshtoso semantic roles from a pinned stylesheet loaded after Goshtoso; X-9 does not modify the Goshtoso or Minimal base themes, and both remain selectable. Every theme supplies light and dark semantic counterparts.

Identity assets are pinned from `araihu/assets@ab01f1a0f592e4f1398173df04e4f8fc013cb21a`. Desktop light mode uses the canonical Xisnove V10 logo; compact/mobile and dark compositions use the matching normal or reverse mark. Do not redraw, recolor, or substitute the Arai Hû cloud.

**The Signal Rarity Rule.** Primary color identifies navigation, focus, selection, and the most important action. It does not wash large passive surfaces.

**The Evidence Rule.** Success, warning, danger, pending, and unknown use their semantic text/background roles with an explicit word or symbol.

## Typography

Use the active Goshtoso theme’s sans-serif stack. Headlines are firm and compact; body text stays plain and readable; operational labels carry enough weight to survive dense lists. IDs, timestamps, endpoints, and probe evidence may use the theme’s monospace role when available.

Keep page descriptions short. Prefer direct labels such as “Last checked”, “Observed from”, and “3 locations reporting” over implementation language.

## Layout

Authenticated routes share a persistent AppShell. Desktop uses a compact navigation rail, a flexible collection workspace, and a viewport-owned right drawer when an item is selected. Mobile uses one navigation trigger and a full-height overlay below the shell header; detail remains a drawer rather than appearing inline after the table.

The top bar owns global navigation and resource search opened by `Ctrl+K` or `⌘K`. Static navigation destinations may be resolved locally; operational resources are queried and ranked by the server. Page toolbars contain only collection-local filters, refresh, and primary actions. At 1440 px the operations list is the dominant surface; at 390 px it becomes a readable stacked list instead of a horizontally compressed desktop table.

Spacing follows a 4/8/16/24/32 rhythm. One main region scrolls; nested scrolling is reserved for genuine long lists inside dialogs or drawers.

## Elevation & Depth

X-9 is flat by default. Borders, tonal surface changes, and selected-route indicators carry structure. Elevation is reserved for viewport-owned layers: global search, mobile navigation, and the inspection drawer. Do not add shadows to every list or panel.

## Shapes

Use theme-owned radii and control outlines. Long rules and aligned columns evoke a route board more effectively than decorative stripes. Status badges are compact and readable; controls keep a minimum 44 by 44 pixel target even when their visual content is smaller.

## Components

Use public Goshtoso components instead of reproducing their internals.

- **AppShell:** owns the persistent header, navigation, skip link, and primary scroll region.
- **Search:** one global instance in the top bar, with `GlobalShortcut`, a stable trigger/modal ID, server-backed typeahead, bounded results, and explicit loading, error, retry, and empty states. Preserve Manja's compact metadata-rich result composition, not its download-once client index.
- **Toolbar:** compact page-local filters and actions only; never a second global search owner.
- **Operations list:** monitor, incident, discovery, and agent collections share selected identity, loading, empty, partial, error, and continuation behavior.
- **Drawer:** right-side inspection workspace synchronized with canonical URL, selected row, focus, Back/Forward, and fresh server reads.
- **Badges and alerts:** always pair tone with meaningful text; unknown and stale are first-class states.
- **Buttons and forms:** preserve native semantics, loading text, disabled targets, error fragments, and minimum touch size.

## Do's and Don'ts

### Do:

- **Do** lead with current operational state, evidence freshness, and the next safe action.
- **Do** preserve the same selected identity across URL, row, drawer, focus, and ARIA state.
- **Do** render loading, empty, partial, unknown, error, and success as designed states.
- **Do** verify 390/1440, Goshtoso/Minimal, light/dark, keyboard, focus, and console behavior.

### Don't:

- **Don't** download the operational corpus for client-side filtering or imply that a bounded result page is exhaustive.
- **Don't** use decorative dashboards, gratuitous gradients, or repeated cards for data that belongs in a list.
- **Don't** invent history, discovery decisions, or live-stream contracts absent from the public API.
- **Don't** patch copied Goshtoso internals inside X-9; isolate any library extension upstream.
