"""Generate deterministic README assets that mirror Charta's terminal layout."""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "docs" / "assets"
WIDTH, HEIGHT = 1280, 720

COLORS = {
    "background": "#080b14",
    "terminal": "#111827",
    "border": "#334155",
    "active": "#22d3ee",
    "keyword": "#c084fc",
    "function": "#67e8f9",
    "string": "#facc15",
    "number": "#fb7185",
    "text": "#e5e7eb",
    "muted": "#94a3b8",
    "selection": "#1e3a5f",
    "header": "#0f172a",
    "success": "#86efac",
}


def font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont:
    candidates = [
        Path("C:/Windows/Fonts/consolab.ttf" if bold else "C:/Windows/Fonts/consola.ttf"),
        Path("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf" if bold else "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"),
    ]
    for candidate in candidates:
        if candidate.exists():
            return ImageFont.truetype(str(candidate), size)
    return ImageFont.load_default()


REGULAR = font(22)
SMALL = font(18)
BOLD = font(21, bold=True)


def text(draw: ImageDraw.ImageDraw, xy: tuple[int, int], value: str, color: str = "text", *, face=REGULAR) -> None:
    draw.text(xy, value, fill=COLORS.get(color, color), font=face)


def frame(stage: int) -> Image.Image:
    image = Image.new("RGB", (WIDTH, HEIGHT), COLORS["background"])
    draw = ImageDraw.Draw(image)
    draw.rounded_rectangle((24, 22, WIDTH - 24, HEIGHT - 22), 18, fill=COLORS["terminal"], outline=COLORS["border"], width=2)
    draw.rounded_rectangle((24, 22, WIDTH - 24, 68), 18, fill=COLORS["header"])
    draw.rectangle((24, 50, WIDTH - 24, 68), fill=COLORS["header"])
    for x, color in ((52, "#fb7185"), (82, "#facc15"), (112, "#4ade80")):
        draw.ellipse((x, 37, x + 14, 51), fill=color)
    text(draw, (162, 31), "charta  SQLite: Portfolio demo", "text", face=BOLD)

    browser_right = 315
    draw.line((browser_right, 68, browser_right, HEIGHT - 66), fill=COLORS["border"], width=2)
    text(draw, (48, 88), "Connections", "active", face=BOLD)
    text(draw, (48, 127), "▾ Portfolio demo", face=SMALL)
    text(draw, (70, 160), "▾ main  default", "success", face=SMALL)
    text(draw, (92, 193), "▸ projects", face=SMALL)
    text(draw, (92, 226), "  sqlite_sequence", "muted", face=SMALL)
    text(draw, (48, 600), "a add   e edit", "muted", face=SMALL)
    text(draw, (48, 629), "t test  r refresh", "muted", face=SMALL)

    editor_bottom = 405
    draw.line((browser_right, editor_bottom, WIDTH - 24, editor_bottom), fill=COLORS["border"], width=2)
    text(draw, (339, 82), "SQL editor", "active", face=BOLD)

    if stage == 0:
        lines = [
            [("1 │ ", "muted"), ("select", "keyword"), (" status,", "text")],
            [("2 │ ", "muted"), ("count", "function"), ("(*) projects,", "text"), ("max", "function"), ("(updated_at) last_update", "text")],
            [("3 │ ", "muted"), ("from", "keyword"), (" projects ", "text")],
            [("4 │ ", "muted"), ("group by", "keyword"), (" status ", "text"), ("order by", "keyword"), (" projects ", "text"), ("desc", "keyword"), (";", "text")],
        ]
    else:
        lines = [
            [("1 │ ", "muted"), ("SELECT", "keyword")],
            [("2 │     status,", "text")],
            [("3 │     ", "text"), ("COUNT", "function"), ("(*) ", "text"), ("AS", "keyword"), (" projects,", "text")],
            [("4 │     ", "text"), ("MAX", "function"), ("(updated_at) ", "text"), ("AS", "keyword"), (" last_update", "text")],
            [("5 │ ", "muted"), ("FROM", "keyword"), (" projects", "text")],
            [("6 │ ", "muted"), ("GROUP BY", "keyword"), (" status", "text")],
            [("7 │ ", "muted"), ("ORDER BY", "keyword"), (" projects ", "text"), ("DESC", "keyword"), (";", "text")],
        ]

    y = 122
    for line in lines:
        x = 340
        if stage >= 1 and y in (122, 158, 194, 230, 266, 302, 338):
            draw.rectangle((326, y - 5, WIDTH - 38, y + 29), fill=COLORS["selection"] if stage == 1 else COLORS["terminal"])
        for value, color in line:
            text(draw, (x, y), value, color)
            x += int(draw.textlength(value, font=REGULAR))
        y += 36

    text(draw, (339, 422), "Results", "active", face=BOLD)
    if stage < 2:
        text(draw, (339, 475), "Ctrl+Enter to run the current statement", "muted")
    else:
        headers = ["status", "projects", "last_update"]
        values = [
            ["active", "2", "2026-09-09"],
            ["planned", "1", "2026-09-07"],
        ]
        xs = [342, 620, 820]
        for x, value in zip(xs, headers):
            text(draw, (x, 466), value, "function", face=BOLD)
        draw.line((332, 500, WIDTH - 38, 500), fill=COLORS["border"], width=2)
        for row, values_row in enumerate(values):
            for x, value in zip(xs, values_row):
                text(draw, (x, 518 + row * 42), value, "text")
        text(draw, (339, 618), "2 rows • query 1.8 ms • fetch 0.4 ms", "success", face=SMALL)

    draw.rectangle((24, HEIGHT - 66, WIDTH - 24, HEIGHT - 22), fill=COLORS["header"])
    status = "SQLite [main] | Ln 7, Col 24 | UTF-8 | Errors: 0"
    if stage == 0:
        status = "SQLite [main] | Ln 4, Col 57 | Ctrl+Shift+F Format"
    elif stage == 1:
        status = "SQL formatted | Ctrl+Enter Run | F8 Errors"
    text(draw, (46, HEIGHT - 57), status, "muted", face=SMALL)
    return image


def main() -> None:
    ASSETS.mkdir(parents=True, exist_ok=True)
    frames = [frame(stage) for stage in range(3)]
    frames[-1].save(ASSETS / "charta-screenshot.png", optimize=True)
    frames[0].save(
        ASSETS / "charta-demo.gif",
        save_all=True,
        append_images=frames[1:] + [frames[-1]],
        duration=[1100, 900, 2200, 1200],
        loop=0,
        optimize=True,
    )


if __name__ == "__main__":
    main()
