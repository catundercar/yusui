# YuSui Web Design System

Decision record for the front-end visual language. Implemented as CSS custom
properties overriding Element Plus design tokens in `src/styles/theme.css`, plus
scoped component styles. Single CSS strategy: CSS variables + scoped CSS. No
Tailwind, no CSS-in-JS.

## 1. Theme and atmosphere

A light "control-plane" console for zero-trust ops: professional, calm, high
information density, zero decoration. Near-white cool canvas, hairline-bordered
white surfaces, one disciplined indigo accent. Every machine value (IDs, IPs,
ports, timestamps, the terminal) is set in monospace. The terminal is the one
dark island inside the light app. Light single-theme by decision; the terminal
stays dark regardless.

## 2. Color palette and roles

| Token | Value | Role |
|---|---|---|
| `--ys-accent` | `#4f46e5` | primary action, active nav, focus ring, brand mark (indigo) |
| `--ys-canvas` | `#f6f7f9` | app background |
| `--ys-surface` | `#ffffff` | panels, cards, top bar |
| `--ys-text` | `#1e2430` | primary text |
| `--ys-muted` | `#6b7382` | secondary text, table headers, machine timestamps |
| `--ys-border` | `#e2e6ec` | hairline dividers and surface edges |
| success / warning / danger / info | `#16a34a` / `#d97706` / `#dc2626` / `#64748b` | status dots and semantic buttons |

The full Element Plus ramps (primary + semantic `light-3/8/9`, `dark-2`) are
overridden so hovers and soft backgrounds stay in-palette. Status uses a colored
dot + label, never gray text on a colored fill.

## 3. Typography

- UI: **Geist Variable** (self-hosted via `@fontsource-variable/geist`). Chosen
  for engineered, neutral-but-distinct technical character; excellent at 13-14px
  in dense tables. CJK falls back to PingFang SC / Noto Sans SC.
- Machine values + terminal: **Geist Mono Variable**.
- Base 14px / tables 13px. Page titles 20px weight 600, letter-spacing -0.012em.
- `tabular-nums` on IDs, ports, timestamps. Font smoothing on once at root.

## 4. Components (states)

- **Button**: weight 500, radius 6px, `active:scale(0.97)`, 120ms ease-out color
  transitions. Primary carries a faint indigo shadow. Danger actions use `plain`.
- **Input / select**: 1px inset border, hover darkens, focus = indigo inset +
  3px soft indigo ring.
- **Panel**: white surface, 1px `--ys-border`, radius 10px, `--ys-shadow-card`.
- **Table**: quiet `#fafbfc` header in muted weight-600, hairline rows, hover tint.
- **Status**: `.ys-status` = colored dot + localized label; raw enum on `data-status`.
- **Nav**: understated horizontal menu, indigo text + 2px underline when active.

## 5. Layout

- Top bar 56px: brand mark + name, nav, language switch, user chip, sign-out.
- Content max-width 1180px, centered, 28px/24px padding on canvas.
- Spacing rhythm in multiples of 4; form-card groups inputs above its table.

## 6. Depth

Light mode: separation by 1px hairline border + a single soft shadow
(`0 1px 2px / 0 1px 3px rgba(20,30,50,~.06)`). No heavy shadows, no glass.
Dark terminal: luminance steps via `rgba(255,255,255,.07)` borders on near-black.

## 7. Do / Don't

- Do put every machine value in mono with tabular figures.
- Do express state as a colored dot + word, with the raw enum on `data-*`.
- Do keep one indigo accent; semantic colors only for status and intent.
- Don't add card shadows as default containers; panels are the only elevated surface.
- Don't use gradients (the only exception: one faint indigo wash at the top of login).
- Don't localize the raw status/role enum; localize the label, keep the enum in `data-*`.

## 8. Responsive

Single breakpoint at 640px: hide brand name + user meta + avatar, tighten the top
bar and nav padding so it never overflows; data tables scroll horizontally inside
their panel. Honors `prefers-reduced-motion` (route fade and terminal pulse off).

## 9. i18n

vue-i18n, `zh` default + `en`, switchable from the top bar and login (persisted
in `localStorage`). Element Plus built-in strings follow via `el-config-provider`.
zh message values for anything an e2e test selects on (button labels,
placeholders, tab names, dialog titles, terminal banners) are kept identical to
the original hardcoded strings on purpose; language-variant assertions in tests
pin to `data-status` / `data-role` instead.

## Aesthetic direction (named)

Light control-plane, indigo. Geist + Geist Mono give it an engineered, technical
voice without the reflex-font look; the single indigo accent plus dot-status and
monospace machine values make it read as an operator console, not a generic admin
template.
