# Appearance with ui.conf

The Mac app's look comes from one text file, `ui.conf`, kept on the computer that runs Agenthail at `~/.agenthail/ui/ui.conf`. You can edit it by hand, change it with `agenthail ui`, or ask an agent to change it. The app picks up a change within a few seconds of it being saved.

`~/.agenthail/ui` is a git repository. Every change is a commit that records who made it (you, or the agent that ran `agenthail ui`) and why, so any change can be listed, compared and undone.

## Format

One setting per line, `key = value`. Lines starting with `#` are comments, and ` # ` after a value starts a comment.

```
version = 1
theme = dark
background = art: dawn
glass.blur = 24
statusbar = working, needs-you, hosts
```

A setting can apply to one kind of device or to one named device:

```
background = art: tide
background@phone = none
glass@device:Studio = off
```

The device kinds are `mac`, `phone`, `tablet` and `web`. `device:<name>` matches a computer by its name; the Mac app uses the name in System Settings > General > Sharing. For each device, `key@device:<name>` beats `key@<kind>`, which beats `key`, which beats the default. When the same key and device appear twice, the later line wins.

A line Agenthail can't use, such as an unknown key or a value of the wrong kind, is skipped and reported with its line number; the rest of the file still applies. A number outside its range is reported and set to the nearest allowed value.

## Settings

`agenthail ui show-defaults --docs` prints this list with each default.

| Key | Default | Values |
|---|---|---|
| `version` | `1` | File format version. |
| `theme` | `system` | `system`, `dark`, `light`. |
| `theme.accent` | `system` | Accent for links, focus and working state: `system` or `#rrggbb`. |
| `background` | `art: tide` | What fills the window. See [Backgrounds](#backgrounds). |
| `background.fit` | `cover` | How an image fills the window: `cover`, `contain`, `center`, `tile`. |
| `background.position` | `center` | Which part of an image stays in view: `center`, `top`, `bottom`, `left`, `right`. |
| `background.dim` | `0` | 0 to 0.8. Darkens the background in dark appearance, lightens it in light, before anything else. |
| `background.opacity` | `1` | 0 to 1. Background strength on New Session and empty views. |
| `background.session-opacity` | `1` | 0 to 1. Background strength inside a session. |
| `glass` | `on` | `on` shows translucent panes over the background; `off` uses solid surfaces. |
| `glass.blur` | `30` | 0 to 64. Blur radius in points behind panes. |
| `glass.tint` | `ground` | Tint of panes: `ground`, `accent` or `#rrggbb`. |
| `glass.opacity` | `0.18` | 0 to 1. Tint of the content pane. This is a minimum: it is raised around text where the text needs it, and margins and empty areas keep this tint. |
| `glass.sidebar.opacity` | `0.45` | 0 to 1. Tint of the sidebar. |
| `glass.chrome.opacity` | `0` | 0 to 1. Tint of the title bar and status strip. |
| `glass.composer.opacity` | `0.5` | 0 to 1. Tint of floating glass: the message box, tab plates and popovers. With `glass.readability = auto` it is at least 0.2, the least that keeps their text readable over glass, and more when `contrast.target` is higher. |
| `glass.readability` | `auto` | `auto` raises the tint of panes, and adds a layer on open areas, only where text needs it to reach `contrast.target`. `off` uses the glass opacities exactly, so text may be harder to read. With Increase Contrast on in Accessibility settings, the app always uses `auto`. |
| `glass.border` | `0.08` | 0 to 0.3. Opacity of pane hairlines. |
| `glass.gutter` | `8` | 0 to 16. Points between floating panes; 0 joins them edge to edge. |
| `texture` | `none` | Texture over the background, never over panes or text: `none`, `scanlines`, `grain`. |
| `texture.strength` | `0.2` | 0 to 0.6. How strongly the texture darkens. |
| `texture.spacing` | `3` | 2 to 8. Points between scanlines. |
| `scrim.tint` | `auto` | Colour of the layer that keeps text readable: `auto` takes the background's own hue, darkened; or `black` or `#rrggbb`. |
| `scrim.feather` | `240` | 40 to 320. Points over which that layer fades out around text on open areas. |
| `contrast.target` | `4.5` | 4.5 to 7. Minimum contrast for every kind of text. With Increase Contrast on in Accessibility settings, the app uses 7. |
| `titlebar.tabs` | `sessions` | `sessions` opens sessions as tabs in the title bar; `none` turns tabs off. |
| `titlebar.tabs.label` | `alias-title` | What a tab shows: `alias-title`, `alias`, `title`. |
| `titlebar.tabs.preview` | `on` | `on`: a sidebar click opens a preview tab that the next click replaces. |
| `statusbar` | `usage, working, needs-you, hosts, terminal` | Items in the bottom strip, in order, or `none`. |

Whatever the background, text keeps at least `contrast.target` contrast: the app measures the background behind text and darkens or lightens it as much as needed, around the text only, so the background stays visible in margins, gaps and empty views. `glass.readability = off` turns this off.

## Backgrounds

- `none`: the plain window colour.
- `desktop`: the desktop picture shows through.
- `art: <name>`: a built-in picture: `tide`, `aurora`, `dawn` or `field`. Each has a dark and a light version, and the app shows the one that matches its appearance.
- `image: <path>`: a PNG, JPEG, HEIC or WebP image under 30 MB. `agenthail ui set` copies an image from anywhere into `~/.agenthail/ui/backgrounds/` and writes that path, so the image is versioned with the file. The Mac app's Appearance settings upload a chosen or dropped image the same way, also to a remote computer.
- `gradient: <preset>`: one of `dusk`, `slate`, `forest`, `ember`, `violet`, `mono`. Each has a dark and a light version, like the built-in pictures.
- `gradient: <angle>deg, <colour> <pos>%, <colour> <pos>%, ...`: a linear gradient with at least two stops, for example `gradient: 135deg, #1b2230 0%, #4a2c8a 100%`.
- `mesh: <cols>x<rows>, <colour>, ...`: a mesh from 2x2 to 6x6, with one `#rrggbb` colour per point, row by row. Each preset is a pair of 3x3 meshes; `agenthail ui show-defaults --docs` prints them to copy and edit.

A background you write yourself, other than a built-in picture or preset, looks the same in dark and light. `agenthail ui get --resolved` shows both as `background` and `background.light`.

## The agenthail ui command

```
agenthail ui get [--resolved] [--device mac|phone|tablet|web] [--name <device>] [--json]
agenthail ui set 'key = value' ... [--message <text>]
agenthail ui unset <key>[@device] ... [--message <text>]
agenthail ui write <file|-> [--message <text>]
agenthail ui check <file|->
agenthail ui log [--limit <n>] [--json]
agenthail ui diff [<rev>]
agenthail ui undo [<rev>]
agenthail ui show-defaults [--docs]
agenthail ui path
agenthail ui snapshot [--session <id>] [--size <W>x<H>] [--name <device>] <out.png>
```

- `get` prints the file; `--resolved` prints the value of every key for one device after overrides and defaults.
- `set` and `unset` change single lines and keep the rest of the file, comments included. `--message` says why; it becomes the commit message.
- `set` refuses an unknown key or a bad value. `write` replaces the whole file and refuses a file with any problem, listing the lines to fix. A problem already in the file from a hand edit stays as it is through `set` and `unset`, and is still reported and skipped.
- `check` reports problems in a file without saving it.
- `log` lists changes, newest first, with who made each one. `diff` shows what a change did, or what changed since the last one.
- `undo` reverts the last change, or the one you name, as a new change. If a later change edited the same lines, it says so and changes nothing.
- `path` prints where `ui.conf` is.
- `snapshot` saves a PNG of the Mac app's main window, so you or an agent can see the result of a change. See [Snapshots](#snapshots).

An edit made by hand in a text editor is committed as your change once you save the file.

## API

Apps read and change `ui.conf` through the daemon, with the same checks as `agenthail ui`:

- `GET /api/v1/ui?class=<kind>&device=<name>` (`read` scope) returns `values`, every key resolved for that device with `background` and `background.light` ready to draw; `chosen`, each key as `ui.conf` writes it for that device; `overrides`, the device (`mac` or `device:<name>`) of each value that comes from a `key@<device>` line; and `problems`.
- `POST /api/v1/ui` (`settings` scope) with `{"action": "set", "set": ["key = value", ...]}`, `{"action": "unset", "unset": ["key@device", ...]}`, `{"action": "write", "text": ...}` or `{"action": "undo", "rev": ...}`, each with an optional `message`.
- `GET /api/v1/ui/log` (`read` scope) lists changes.
- `GET /api/v1/ui/presets` (`read` scope) lists the built-in pictures and gradient presets, each with the `value` to write and what it draws in dark and light.
- `GET /api/v1/ui/background?path=<path>` (`read` scope) returns an image from `background`, including the built-in pictures under `art/`.
- `POST /api/v1/ui/background?name=<file name>` (`settings` scope) stores the request body as a background image and returns `{"path"}` to use as `image: <path>`. A name that is not an image, or a `Content-Length` over 30 MB, is refused before the body is read.

## Snapshots

`agenthail ui snapshot <out.png>` asks the Mac app connected to the daemon for a picture of its main window and writes it to `out.png`. The app answers without bringing anything to the front or changing its windows, and an occluded window is captured whole. The picture is what the window server composites, so glass, blur and the background look exactly as they do on screen.

- With no options it captures the main window as it is now. If the main window is closed or minimized, it draws the session the main window last showed, at the main window's size or 1440x900 points if it has none.
- `--session <id>` and `--size <W>x<H>` draw a separate copy of the main window, in points, that nobody sees. The window you are using keeps its session, size and layout. A size below the window's minimum is widened to that minimum, as the real window would be.
- `--name <device>` picks the Mac by its name when more than one Mac app is connected. Otherwise the first app to answer is used.
- The command waits up to 20 seconds and says so if no app answered.
- The capture uses macOS's `CGWindowListCreateImage`. If a future macOS removes it, the command fails with a message saying so rather than saving a less faithful picture.

The API behind it is `POST /api/v1/ui/snapshot` with an optional JSON body `{"session", "width", "height", "device"}`; the response is the PNG. It needs the `read` scope. A snapshot can show any session visible in the window, so anything allowed to read sessions can take one. Connected apps receive the request as a `ui.snapshot.requested` event carrying `deadlineMs`, the time the daemon will wait counted from when it sent the event, and answer with `POST /api/v1/ui/snapshot/<id>`. Requests are held only in memory for 20 seconds; when one is answered or given up, the daemon sends `ui.snapshot.closed` with the request id as `entityId`, so an app replaying events stops work on a request that has already ended.
