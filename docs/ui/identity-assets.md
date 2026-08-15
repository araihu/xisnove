# Xisnove identity assets

The canonical Xisnove V10 SVG set is copied byte-for-byte from
`araihu/assets@ab01f1a0f592e4f1398173df04e4f8fc013cb21a`. Only Arai Hû uses the
organization cloud; Xisnove uses its independent monitoring-signal symbol.

The BFF publishes the current assets on immutable same-origin routes:

- `/ui/xisnove-ab01f1a.svg`
- `/ui/xisnove-logo-ab01f1a.svg`
- `/ui/xisnove-mark-ab01f1a.svg`
- `/ui/xisnove-mark-reverse-ab01f1a.svg`

The prior `bffc2ac` and `81300f5` favicon routes remain available during
rolling deployments. Handler tests pin the upstream SHA-256 digest of every
current asset and require `public, max-age=31536000, immutable`.

The authenticated console shell uses the frozen X-9 mark and favicon from
`araihu/assets@74c36ed038ad127cab72d10ac6c5a8ca79646244` through the
versioned seasonal routes:

- `/ui/seasonal/v0.1.1/x9-mark.svg`
- `/ui/seasonal/v0.1.1/x9-mark-reverse.svg`
- `/ui/seasonal/v0.1.1/x9-favicon.svg`

These routes are served by `ui/internal/seasonalassets` and retain immutable
cache headers; the canonical public-status brand above remains available for
the public navbar.
