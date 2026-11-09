# Benbow — brand assets (icon "Round 1")

Pixel-art "C" in the style of 90s arcade fighting-game titles: yellow → orange → red gradient, dark outline, speed lines on a deep purple background. Designed on a 16×16 grid.

## Files

| Path | Use |
|---|---|
| `AppIcon.appiconset/` | Xcode asset catalog for the macOS app (16–512 pt, @1x and @2x), pre-masked with rounded corners and shadow |
| `source/benbow-icon-1024.png` | Master 1024×1024, pre-masked (same as the asset catalog) |
| `source/benbow-icon-fullbleed-1024.png` | Full-bleed square artwork, no mask: use as the layer in Icon Composer for macOS 26 layered icons |
| `source/benbow-icon.svg` | Vector master (16×16 grid, rounded clip) — source of truth for the web |
| `source/benbow-icon-grid.json` | The pixel grid and palette as data, to regenerate any size |
| `menubar/benbow-menubar-template*.png/.svg` | Menu bar icon: black C on transparent, to be used as a template image (`isTemplate = true`) so macOS tints it for light/dark menu bars |
| `web/` | `favicon.svg`, `favicon.ico`, `favicon-32.png`, `apple-touch-icon.png` (180), `icon-512.png` |

## Palette

| Token | Hex | Role |
|---|---|---|
| ink | `#1A0B2E` | Background |
| ink-light | `#2A1450` | Speed lines |
| outline | `#2B0A0A` | Letter outline |
| yellow | `#FFD23F` | Top of the letter, sparks |
| orange | `#F28C28` | Middle of the letter |
| red | `#E63946` | Bottom of the letter |
| cream | `#FFF4D6` | Highlight |

## Rules

- Always scale the pixel art with nearest-neighbour (no smoothing) when the size is a multiple of 16; for other sizes use the provided PNGs.
- Do not redraw, rotate or recolour the C. Do not add text inside the icon.
- The display font for headings in arcade style is "Press Start 2P" (Google Fonts, OFL); UI text stays in the system font.
- Name and logo are covered by TRADEMARKS.md.
