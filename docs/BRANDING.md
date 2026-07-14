<p align="center"><img src="../assets/logo.svg" width="96" alt="mneme"></p>

# mneme -- Branding & Visual Identity

The visual identity of mneme in one page: what the mark means, the official
palette, when to use each variant, and copyable SVG source. If you are adding a
logo to a page, a slide, or a social card, start here.

Back to the [README](../README.md).

---

## The mark

The mneme logo is a **graph whose nodes and edges draw the letter "m"**. Four
outer nodes and the connecting stroke trace the silhouette of an "m"; the fifth,
larger node sits at the centre in teal -- the **active memory**, the single
recollection currently in context.

The mark is deliberately literal about what mneme is: a *memory* system built on
a *knowledge graph*. Nodes are memories, edges are relations, and the highlighted
centre is retrieval bringing one memory forward. One accent colour, no gradients,
no glow -- structure over decoration.

Geometry (viewBox `0 0 100 100`):

- Stroke path: `M26 72V33l24 20 24-20v39`, `stroke-width` 4.2, round caps/joins.
- Four outer nodes `r=6` at (26,72), (26,33), (74,33), (74,72).
- One centre node `r=9` at (50,53) -- always teal.

---

## Variants

| Variant | File | When to use |
|---------|------|-------------|
| Color | [`../assets/logo.svg`](../assets/logo.svg) | Default. Light backgrounds, README hero, docs, slides. |
| Monochrome | [`../assets/logo-mono.svg`](../assets/logo-mono.svg) | Inherits `currentColor` -- use on coloured/dark surfaces, single-colour print, or anywhere the teal accent cannot render. |
| Favicon | [`../assets/favicon.svg`](../assets/favicon.svg) | 32x32 square tab/app icon. Same color mark, tuned for small sizes. |
| Wordmark | [`../assets/wordmark.svg`](../assets/wordmark.svg) | Mark + "mneme" lockup for headers, banners, and social cards where the name must appear alongside the mark. |

The monochrome variant paints every element with `currentColor`, so it adopts the
surrounding text colour automatically -- ideal for embedding inline or on
arbitrary backgrounds.

---

## Palette

A single teal accent against a cool neutral ink. Every value has a light-mode and
a dark-mode counterpart. Never introduce a second accent and never use gradients.

| Role | Light | Dark | Notes |
|------|-------|------|-------|
| Accent (teal) | `#0d8f80` | `#2fd4bf` | Centre node and brand colour. The only accent. |
| Node / neutral | `#3a4654` | `#aeb9c7` | Outer nodes and connecting stroke. |
| Ink (text) | `#14181f` | `#e9edf3` | Primary text, including the wordmark body. |
| Muted | `#5b6572` | `#8b96a5` | Secondary text, captions. |

The centre node stays teal in both the color and favicon variants regardless of
mode. In the monochrome variant there is no accent -- the whole mark is one
colour by design.

---

## Typography

The wordmark sets **"mneme"** in a system sans-serif at semi-bold weight
(`font-weight: 600`) with a slight negative letter-spacing for a compact,
engineered feel:

```
-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif
```

Using the platform system font keeps the SVG self-contained (no embedded font
files) and renders crisply everywhere. The body of the word is ink
(`#14181f`); the central "m" is teal (`#0d8f80`) so the wordmark echoes the
mark's teal centre node -- the same "active memory" cue carried into the text.

---

## Do's & don'ts

**Do**

- Keep clear space around the mark equal to the diameter of an outer node.
- Use the monochrome variant on coloured or photographic backgrounds.
- Preserve the teal centre node -- it is the heart of the identity.
- Scale the SVG freely; it is resolution-independent.

**Don't**

- Don't add a second accent colour or any gradient.
- Don't recolour the centre node to anything but the teal accent.
- Don't rotate, skew, or redraw the graph geometry.
- Don't add drop shadows, outlines, or glows.
- Don't stretch the mark non-proportionally.

---

## SVG source

Copyable source for both primary variants. Both are valid, self-contained XML
with `role="img"` and `aria-label="mneme"`.

### Monochrome (inherits `currentColor`)

```xml
<svg width="96" height="96" viewBox="0 0 100 100" fill="none" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="mneme">
  <g stroke="currentColor" stroke-width="4.2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M26 72V33l24 20 24-20v39"/>
  </g>
  <g fill="currentColor">
    <circle cx="26" cy="72" r="6"/><circle cx="26" cy="33" r="6"/>
    <circle cx="74" cy="33" r="6"/><circle cx="74" cy="72" r="6"/>
    <circle cx="50" cy="53" r="9"/>
  </g>
</svg>
```

### Color

```xml
<svg width="96" height="96" viewBox="0 0 100 100" fill="none" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="mneme">
  <g stroke="#3a4654" stroke-width="4.2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M26 72V33l24 20 24-20v39"/>
  </g>
  <g fill="#3a4654">
    <circle cx="26" cy="72" r="6"/><circle cx="26" cy="33" r="6"/>
    <circle cx="74" cy="33" r="6"/><circle cx="74" cy="72" r="6"/>
  </g>
  <circle cx="50" cy="53" r="9" fill="#0d8f80"/>
</svg>
```

---

Back to the [README](../README.md).
