# Babble: Data Sonification for Claude Code

## Overview

Babble turns Claude Code activity into ambient sound. Inspired by [Choir.io](https://corte.si/posts/choir/intro/) (2013), it tails Claude Code session logs, classifies events into categories, and streams them to a browser that plays sounds from curated sound packs.

The goal is ambient awareness — you hear Claude's "metabolism" while you work, without needing to watch a terminal.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Go Binary                          │
│                                                         │
│  ┌─────────────────────────────────┐                    │
│  │ Session Manager                 │                    │
│  │ (watches ~/.claude/projects/)   │                    │
│  │                                 │                    │
│  │  ┌──────────┐  ┌──────────┐    │    ┌───────────┐  │
│  │  │ Session A │  │ Session B │ ...│───▶│ Classifier│  │
│  │  │ (tail)   │  │ (tail)   │    │    └─────┬─────┘  │
│  │  └──────────┘  └──────────┘    │          │         │
│  └─────────────────────────────────┘          │         │
│                                               │         │
│  ┌─────────────┐    ┌──────────────┐          │         │
│  │ HTTP Server  │    │ WebSocket    │◀─────────┘         │
│  │ (static UI + │    │ Hub          │                    │
│  │  sound packs)│    │ (broadcast)  │                    │
│  └─────────────┘    └──────────────┘                    │
└─────────────────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
┌─────────────────────────────────────────────────────────┐
│                    Browser UI                           │
│                                                         │
│  ┌──────────────┐  ┌───────────┐  ┌──────────────────┐ │
│  │ Audio Engine  │  │ Session   │  │ Event Stream     │ │
│  │ (Web Audio    │  │ Sidebar   │  │ (live scrolling  │ │
│  │  API, plays   │  │ (all      │  │  feed, filtered  │ │
│  │  from packs)  │  │  sessions)│  │  by session)     │ │
│  └──────────────┘  └───────────┘  └──────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

**Stack:** Go backend (single binary), vanilla JS/HTML/CSS frontend, Web Audio API for playback.

## Data Source

Babble reads Claude Code session logs from `~/.claude/projects/`. These are JSONL files where each line is a JSON object representing a session event.

Key event types observed in the JSONL format:

| JSONL field | Values seen |
|-------------|-------------|
| `type` | `user`, `assistant`, `system`, `progress`, `file-history-snapshot` |
| `message.content[].type` | `text`, `thinking`, `tool_use` |
| `tool_use.name` | `Read`, `Edit`, `Write`, `Bash`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `Task`, `AskUserQuestion`, etc. |
| `data.type` | `hook_progress` |

Each line also contains: `sessionId`, `timestamp`, `cwd`, `parentUuid`, `uuid`.

## Event Model

### Categories

Events are classified into 9 categories (analogous to log levels):

| Category | Meaning | Default triggers |
|----------|---------|-----------------|
| `ambient` | Claude is thinking/processing | `thinking` blocks, `text` blocks |
| `action` | Generic tool activity | `Bash` tool use |
| `read` | Reading/searching files | `Read`, `Grep`, `Glob` tool use |
| `write` | Creating/modifying files | `Edit`, `Write` tool use |
| `network` | External communication | `WebFetch`, `WebSearch` tool use |
| `success` | Something completed well | Tool result with success |
| `warn` | Needs attention | Waiting for user input |
| `error` | Something failed | Tool result with error |
| `meta` | Session lifecycle | `Task`, session start, `progress` events |

### JSONL-to-Category Mapping

| JSONL pattern | Category |
|---------------|----------|
| `type: assistant` + content block `type: thinking` | `ambient` |
| `type: assistant` + content block `type: text` | `ambient` |
| `type: assistant` + `tool_use: Read` | `read` |
| `type: assistant` + `tool_use: Grep` | `read` |
| `type: assistant` + `tool_use: Glob` | `read` |
| `type: assistant` + `tool_use: Edit` | `write` |
| `type: assistant` + `tool_use: Write` | `write` |
| `type: assistant` + `tool_use: Bash` | `action` |
| `type: assistant` + `tool_use: WebFetch` | `network` |
| `type: assistant` + `tool_use: WebSearch` | `network` |
| `type: assistant` + `tool_use: Task` | `meta` |
| `type: assistant` + `tool_use: AskUserQuestion` | `warn` |
| `type: user` (new input received) | `warn` clear |
| `type: progress` + `hook_progress` | `meta` |
| `type: system` | `meta` |
| Tool result containing error | `error` |
| Tool result with success | `success` |

Users can override any of these mappings in the config.

## Sound Pack Format

Sound packs are directories containing audio files and a manifest:

```
~/.config/babble/soundpacks/
  ocean/
    pack.json
    ambient.mp3
    action.mp3
    read.mp3
    write.mp3
    write2.mp3
    network.mp3
    success.mp3
    warn.mp3
    error.mp3
    meta.mp3
```

### pack.json

```json
{
  "name": "Ocean",
  "description": "Gentle ocean sounds for your coding session",
  "author": "babble",
  "version": "1.0.0",
  "categories": {
    "ambient":  { "files": ["ambient.mp3"], "loop": true,  "volume": 0.3 },
    "action":   { "files": ["action.mp3"],  "loop": false, "volume": 0.6 },
    "read":     { "files": ["read.mp3"],    "loop": false, "volume": 0.5 },
    "write":    { "files": ["write.mp3", "write2.mp3"], "loop": false, "volume": 0.6 },
    "network":  { "files": ["network.mp3"], "loop": false, "volume": 0.5 },
    "success":  { "files": ["success.mp3"], "loop": false, "volume": 0.7 },
    "warn":     { "files": ["warn.mp3"],    "loop": false, "volume": 0.8 },
    "error":    { "files": ["error.mp3"],   "loop": false, "volume": 0.9 },
    "meta":     { "files": ["meta.mp3"],    "loop": false, "volume": 0.4 }
  }
}
```

- **Multiple files per category**: one is picked randomly each trigger (variety, avoids listener fatigue)
- **`loop: true`**: for ambient sounds that play continuously while Claude is active, fading in/out with activity
- **`volume`**: default volume per category (0.0–1.0), overridable in user config
- A pack can omit categories — those stay silent
- Supported formats: mp3, wav, ogg

Babble ships with one default sound pack. Users can add custom packs by dropping directories into `~/.config/babble/soundpacks/`.

## Multi-Session Support

The Session Manager watches the entire `~/.claude/projects/` directory tree for new or modified `.jsonl` files.

- Spins up a tail goroutine per active session
- Derives a friendly session name from the project path (e.g. `-Users-dacort-src-babble` → `babble`)
- Tracks activity rate per session
- Marks sessions as idle after no events for a configurable duration
- All sessions emit events simultaneously — the browser mixes audio from all active sessions

### WebSocket Message Format

```json
{
  "session": "babble",
  "sessionId": "06fd12b0-ad4b-4cd4-9307-b55a3aa65c2c",
  "category": "write",
  "event": "Edit",
  "detail": "main.go:42",
  "timestamp": "2026-02-25T19:42:01Z"
}
```

## Browser UI

```
┌─────────────────────────────────────────────────────────────┐
│  Babble                                  [Ocean ▾] [gear]   │
├────────────┬────────────────────────────────────────────────┤
│ Sessions   │  Event Stream                                  │
│            │                                                │
│ * babble   │  19:42:01  babble   write    Edit main.go      │
│   ||||..   │  19:42:00  babble   read     Read main.go      │
│   [on] 12/m│  19:41:58  3xplore  action   Bash go test      │
│            │  19:41:55  babble   read     Grep "func"       │
│ . 3xplore  │  19:41:50  babble   ambient  Think             │
│   ......   │  19:41:48  babble   success  Bash exit:0       │
│   [off] 0/m│  19:41:30  babble   network  WebFetch          │
│            │  19:41:20  babble   meta     Session start      │
│            │                                                │
├────────────┴────────────────────────────────────────────────┤
│  ambient ||| action ||  read ||  write |||                  │
│  network |   success || warn |||  error ||||                │
└─────────────────────────────────────────────────────────────┘
```

- **Top bar**: App name, sound pack dropdown, settings gear
- **Session sidebar**: Live sessions with activity bars, events/min, per-session mute toggle. Click to filter stream.
- **Event stream**: Live scrolling feed, color-coded by session
- **Bottom bar**: Volume sliders per category for quick mix adjustments

### Settings Panel (gear icon)

- Event-to-category mapping overrides
- Sound pack management (browse installed packs)
- General settings (port, auto-open browser, watch paths)

## Configuration

User preferences persist to `~/.config/babble/config.json`:

```json
{
  "port": 3333,
  "autoOpen": true,
  "activePack": "ocean",
  "watchPath": "~/.claude/projects",
  "idleTimeout": "5m",
  "categoryVolumes": {
    "ambient": 0.3,
    "action": 0.6
  },
  "mutedSessions": [],
  "eventOverrides": {
    "Bash": "write"
  }
}
```

## CLI

```
babble serve          # Start server on localhost:3333 (default)
babble serve -p 8080  # Custom port
babble packs          # List installed sound packs
babble packs add <dir># Install a sound pack from a directory
```

## Future Possibilities (Not in v1)

- Synthesized sound packs (Web Audio API oscillators instead of samples)
- Sound pack marketplace / sharing
- Stereo panning (different sessions in different ears)
- Activity heatmap visualization
- Claude Code hook integration for lower-latency triggers
- Slack/Discord integration for team sonification
