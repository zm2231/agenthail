# Appearance with ui.conf

The Mac app's look comes from one text file, `ui.conf`, kept on the computer that runs Agenthail at `~/.agenthail/ui/ui.conf`. You can edit it by hand, change it with `agenthail ui`, or ask an agent to change it. The app picks up a change within a few seconds of it being saved.

`~/.agenthail/ui` is a git repository. Every change is a commit that records who made it (you, or the agent that ran `agenthail ui`) and why, so any change can be listed, compared and undone.

## Format

One setting per line, `key = value`. Lines starting with `#` are comments, and ` # ` after a value starts a comment.

```
version = 1
theme = dark
background = gradient: dusk
glass.blur = 24
statusbar = working, needs-you, hosts
```

A setting can apply to one kind of device or to one named device:

```
background = gradient: aurora
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
| `background` | `gradient: aurora` | What fills the window. See [Backgrounds](#backgrounds). |
| `background.fit` | `cover` | How an image fills the window: `cover`, `contain`, `center`, `tile`. |
| `background.position` | `center` | Which part of an image stays in view: `center`, `top`, `bottom`, `left`, `right`. |
| `background.dim` | `0` | 0 to 0.8. Darkens the background in dark appearance, lightens it in light, before anything else. |
| `background.opacity` | `1` | 0 to 1. Background strength on New Session and empty views. |
| `background.session-opacity` | `1` | 0 to 1. Background strength inside a session. |
| `glass` | `on` | `on` shows translucent panes over the background; `off` uses solid surfaces. |
| `glass.blur` | `30` | 0 to 64. Blur radius in points behind panes. |
| `glass.tint` | `ground` | Tint of panes: `ground`, `accent` or `#rrggbb`. |
| `glass.opacity` | `0.18` | 0 to 1. Tint of the content pane. This is a minimum: it is raised where text needs it. |
| `glass.sidebar.opacity` | `0.45` | 0 to 1. Tint of the sidebar. |
| `glass.chrome.opacity` | `0` | 0 to 1. Tint of the title bar and status strip. |
| `glass.composer.opacity` | `0.5` | 0 to 1. Tint of floating glass: the message box, tab plates and popovers. |
| `glass.border` | `0.08` | 0 to 0.3. Opacity of pane hairlines. |
| `glass.gutter` | `8` | 0 to 16. Points between floating panes; 0 joins them edge to edge. |
| `texture` | `scanlines` | Texture over the background, never over panes or text: `none`, `scanlines`, `grain`. |
| `texture.strength` | `0.35` | 0 to 0.6. How strongly the texture darkens. |
| `texture.spacing` | `3` | 2 to 8. Points between scanlines. |
| `scrim.tint` | `auto` | Colour of the layer that keeps text readable: `auto` takes the background's own hue, darkened; or `black` or `#rrggbb`. |
| `scrim.feather` | `240` | 40 to 320. Points over which that layer fades out around text on open areas. |
| `contrast.target` | `4.5` | 4.5 to 7. Minimum contrast for every kind of text. With Increase Contrast on in Accessibility settings, the app uses 7. |
| `titlebar.tabs` | `sessions` | `sessions` opens sessions as tabs in the title bar; `none` turns tabs off. |
| `titlebar.tabs.label` | `alias-title` | What a tab shows: `alias-title`, `alias`, `title`. |
| `titlebar.tabs.preview` | `on` | `on`: a sidebar click opens a preview tab that the next click replaces. |
| `statusbar` | `usage, working, needs-you, hosts, terminal` | Items in the bottom strip, in order, or `none`. |

Whatever the background, panes and text keep at least `contrast.target` contrast: the app measures the background behind text and darkens or lightens it as much as needed.

## Backgrounds

- `none`: the plain window colour.
- `desktop`: the desktop picture shows through.
- `image: <path>`: a PNG, JPEG, HEIC or WebP image under 30 MB. `agenthail ui set` copies an image from anywhere into `~/.agenthail/ui/backgrounds/` and writes that path, so the image is versioned with the file.
- `gradient: <preset>`: one of `aurora`, `dusk`, `slate`, `forest`, `ember`, `violet`, `mono`.
- `gradient: <angle>deg, <colour> <pos>%, <colour> <pos>%, ...`: a linear gradient with at least two stops, for example `gradient: 135deg, #1b2230 0%, #4a2c8a 100%`.
- `mesh: <cols>x<rows>, <colour>, ...`: a mesh from 2x2 to 6x6, with one `#rrggbb` colour per point, row by row. Each preset is a 3x3 mesh; `agenthail ui show-defaults --docs` prints them to copy and edit.

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
```

- `get` prints the file; `--resolved` prints the value of every key for one device after overrides and defaults.
- `set` and `unset` change single lines and keep the rest of the file, comments included. `--message` says why; it becomes the commit message.
- `write` replaces the whole file. `set`, `unset` and `write` refuse a result with any problem and list the lines to fix, so nothing is saved that the apps would have to skip.
- `check` reports problems in a file without saving it.
- `log` lists changes, newest first, with who made each one. `diff` shows what a change did, or what changed since the last one.
- `undo` reverts the last change, or the one you name, as a new change. If a later change edited the same lines, it says so and changes nothing.
- `path` prints where `ui.conf` is.

An edit made by hand in a text editor is committed as your change once you save the file.
