# Benbow — brand assets (icon: the lantern)

The name comes from the Admiral Benbow, the inn in *Treasure Island* where the old
pirate hides with his sea chest. The icon is the inn's lantern: a ship's lantern
with a lit flame, pixel art on a 24×24 grid, deep purple ground with a soft halo.
It is the same lantern that hangs over the captain in the website's hero. A light
that is on means Benbow is running.

## Files

| Path | Use |
|---|---|
| `source/benbow-icon-grid.json` | The pixel grid and palette: source of truth (also in the website, `brand/source/`) |
| `source/benbow-icon.svg` | Vector master (24×24 grid, rounded clip), for the web |
| `source/benbow-icon-1024.png` | Master 1024×1024, pre-masked with rounded corners and shadow (same as the app's iconset) |
| `source/benbow-icon-fullbleed-1024.png` | Full-bleed square artwork, no mask: the layer for Icon Composer (macOS 26 layered icons) |
| `menubar/benbow-menubar-template*.png/.svg` | Menu bar icon: black lantern with the flame cut out, on transparent, 16×16 grid, used as a template image (`isTemplate = true`) |
| `web/` | `favicon.svg`, `favicon.ico`, `favicon-32.png`, `apple-touch-icon.png` (180), `icon-512.png` |

The app's icon is `app/Resources/Benbow.icon` (Icon Composer format: the
full-bleed artwork on the ink fill, no glass). `app/build.sh` compiles it with
Xcode's `actool` into `Assets.car`, the shape macOS 26 expects (a plain icns is
shown on a grey plate there), plus `Benbow.icns` for macOS 14 and 15. Without
Xcode it falls back to `app/Resources/AppIcon.iconset` (16–512 pt, @1x and @2x,
made from `source/benbow-icon-1024.png`, body 832 px at 96 px like Apple's grid).
The menu bar PNGs are copied to `app/Resources/`.

## Wordmark

"Benbow" in Excon Bold (Fontshare, FFL), the website's title font, next to the
icon. No outline or gradient on the wordmark.

## Palette

| Token | Hex | Role |
|---|---|---|
| ink | `#1A0B2E` | Background |
| ink-light | `#2A1450` | Speckles |
| halo | `#3B1D6E`, `#553092` | Lantern halo |
| outline | `#2B0A0A` | Outlines |
| yellow | `#FFD23F` | Flame, sparks |
| orange | `#F28C28` | Glass |
| red | `#E63946` | Bottom of the glass |
| cream | `#FFF4D6` | Flame core |
| wood | `#E3A050`, `#BF7434`, `#8C4A22` | Cap and base |
| iron | `#A29CBA`, `#5E5878` | Ring and frame |

## Rules

- Scale the pixel art with nearest-neighbour only when the size is a multiple of 24; for other sizes use the provided PNGs.
- Do not redraw, rotate or recolour the lantern. Do not add text inside the icon.
- Name and logo are covered by TRADEMARKS.md.
