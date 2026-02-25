# babble

Data sonification for Claude Code, inspired by [Choir.io](https://choir.io). Babble tails your Claude Code session logs, classifies each event into one of nine categories, and streams them to a browser page via WebSocket. The browser translates each event into a short synthesized sound, turning an AI coding session into ambient audio.

## How it works

```
~/.claude/projects/**/*.jsonl
        │
        ▼
  session manager          (Go: fsnotify tail)
        │
        ▼
  event classifier         (Go: ParseLine → Category)
        │
        ▼
  WebSocket hub            (Go: gorilla/websocket broadcast)
        │
        ▼
  browser audio engine     (Web Audio API, sound packs)
```

The Go server embeds the browser UI and the default sound pack as static assets. No external dependencies are needed at runtime.

## Quick start

```
go install github.com/dacort/babble@latest
babble serve
```

Opens `http://localhost:3333`. Start a Claude Code session and hear it come alive.

## Event categories

| Category  | Description                                              |
|-----------|----------------------------------------------------------|
| `ambient` | Thinking blocks and plain text responses                 |
| `action`  | Bash tool use                                            |
| `read`    | Read, Grep, and Glob tool use                            |
| `write`   | Edit, Write, and NotebookEdit tool use                   |
| `network` | WebFetch and WebSearch tool use                          |
| `success` | Tool results that completed without error                |
| `warn`    | AskUserQuestion and human user input turns               |
| `error`   | Tool results with `is_error: true`                       |
| `meta`    | Task, session lifecycle, and progress events             |

## Sound packs

Custom packs live in `~/.config/babble/soundpacks/<pack-name>/`. Each pack contains a `pack.json` manifest.

**Synth pack** (no audio files needed):

```json
{
  "name": "My Pack",
  "description": "Custom synthesized sounds",
  "author": "you",
  "version": "1.0.0",
  "synth": true,
  "categories": {
    "ambient":  { "synth": "sine",  "freq": 220, "duration": 2.0,  "loop": true,  "volume": 0.15 },
    "action":   { "synth": "click", "freq": 800, "duration": 0.05, "loop": false, "volume": 0.4  },
    "read":     { "synth": "sine",  "freq": 440, "duration": 0.1,  "loop": false, "volume": 0.3  },
    "write":    { "synth": "sine",  "freq": 523, "duration": 0.15, "loop": false, "volume": 0.4  },
    "network":  { "synth": "sine",  "freq": 660, "duration": 0.2,  "loop": false, "volume": 0.3  },
    "success":  { "synth": "chord", "freq": 523, "duration": 0.4,  "loop": false, "volume": 0.5  },
    "warn":     { "synth": "saw",   "freq": 330, "duration": 0.3,  "loop": false, "volume": 0.6  },
    "error":    { "synth": "noise", "freq": 200, "duration": 0.3,  "loop": false, "volume": 0.7  },
    "meta":     { "synth": "sine",  "freq": 880, "duration": 0.1,  "loop": false, "volume": 0.2  }
  }
}
```

Available synth types: `sine`, `square`, `sawtooth`, `triangle`, `saw`, `click`, `chord`, `noise`.

**File-based pack**: set `"synth": false` and add `"file": "sound.mp3"` (or `.ogg`, `.wav`) to each category entry. Place the audio files alongside `pack.json`.

The active pack is selected from the browser UI or via `config.json`.

## Configuration

`~/.config/babble/config.json` is created automatically on first run with defaults. Key fields:

| Field             | Default                  | Description                              |
|-------------------|--------------------------|------------------------------------------|
| `port`            | `3333`                   | HTTP server port                         |
| `autoOpen`        | `true`                   | Open browser on `babble serve`           |
| `activePack`      | `"default"`              | Sound pack name to use                   |
| `watchPath`       | `"~/.claude/projects"`   | Directory tree to tail for session logs  |
| `idleTimeout`     | `"5m"`                   | Stop ambient sound after this idle gap   |
| `categoryVolumes` | `{}`                     | Per-category volume overrides (0.0–1.0)  |
| `mutedSessions`   | `[]`                     | Session names to suppress                |
| `eventOverrides`  | `{}`                     | Remap event names to different categories|

## CLI reference

```
babble serve [-p port] [--no-open]

  -p int        Port to listen on (default 3333)
  --no-open     Don't auto-open the browser

babble -version
```
