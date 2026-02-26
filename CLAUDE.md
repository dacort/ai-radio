# Babble

Data sonification for Claude Code. Tails session JSONL logs, classifies events into categories, streams them via WebSocket, and plays sounds in the browser.

## Project structure

- `cmd/` — CLI entry point, web assets (`web/`), embedded sound pack manifests (`soundpacks/`)
- `internal/events/` — JSONL parser, event classification into categories
- `internal/sessions/` — File watcher that tails session logs
- `internal/hub/` — WebSocket broadcast hub
- `internal/server/` — HTTP/WS server, pack API
- `internal/packs/` — Pack manifest loader
- `internal/config/` — User config (~/.config/babble/config.json)

## Event categories

Every JSONL event gets classified into one of these categories. Sound packs map each category to one or more audio files.

| Category | What triggers it | Emotional tone |
|----------|-----------------|----------------|
| `ambient` | Thinking, plain text blocks | Background texture — low volume, often looped |
| `init` | Session start (hook_progress) | Arrival, intro fanfare |
| `action` | Bash tool use | Doing something — punchy, percussive |
| `read` | Read, Grep, Glob tools | Scanning, searching — light, quick |
| `write` | Edit, Write, NotebookEdit tools | Creating, modifying — satisfying impact |
| `network` | WebFetch, WebSearch tools | Reaching out — mysterious, connective |
| `success` | tool_result (no error) | Victory, accomplishment |
| `warn` | User input, AskUserQuestion, compact | Attention needed — cautionary |
| `error` | tool_result with is_error=true | Something broke — dramatic, negative |
| `meta` | Task, plan mode, skill invocations | System plumbing — subtle UI sounds |

## Building sound packs

### Finding sounds

Good sources for retro game sounds (direct download, no auth):

- **classicgaming.cc** — ZIP archives containing WAV files. URL patterns:
  - Donkey Kong: `https://www.classicgaming.cc/classics/donkey-kong/sound-files/{name}.zip`
  - Others: `https://www.classicgaming.cc/classics/{game}/files/sounds/{name}.zip`
- **mortalkombatwarehouse.com** — Direct MP3 downloads. Pattern:
  - `https://www.mortalkombatwarehouse.com/mk1/sounds/{category}/{filename}.mp3`
  - Categories: `announcer/`, `hitsounds/`, `specialfx/`, `explosions/`, `musiccues/`, `ui/`, `scorpion/`, `liukang/`, etc.
- **themushroomkingdom.net** — Direct WAV downloads for Mario:
  - `https://themushroomkingdom.net/sounds/wav/smb/{filename}.wav`
- **noproblo.dayjo.org** — Direct WAV downloads for Zelda:
  - `https://noproblo.dayjo.org/zeldasounds/LOZ/{filename}.wav`

Always verify URLs with `curl -sI <url>` before adding to the registry. Look for HTTP 200.

### Mapping sounds to categories

The goal is to make the soundscape **feel right** — each category should evoke the right emotion without being annoying at high event rates.

**Principles:**
1. **ambient** should be quiet and unobtrusive (volume 0.06-0.08). It loops, so pick something that doesn't grate.
2. **action** fires frequently (every Bash command). Pick short, punchy sounds. Multiple files get random selection, which adds variety.
3. **read** is the most frequent category. Keep it very light and quick — a coin clink, a menu blip.
4. **write** should feel satisfying but not heavy. It fires less than read.
5. **network** is relatively rare. Can be more dramatic or distinctive.
6. **success/error** are the emotional peaks. Success should feel rewarding, error should feel bad. These can be louder.
7. **warn** is for "pay attention" moments. User input prompts, context compaction.
8. **init** only fires once per session. Make it count — a fanfare, a "FIGHT!", a startup jingle.
9. **meta** is system plumbing. Keep it subtle — UI clicks, small notification sounds.

**When picking from a game's sound library, think:**
- What's the game's most iconic sound? That's probably `init` or `success`.
- What sounds like a hit/action? That's `action`.
- What's the failure/death sound? That's `error`.
- What's a warning or alert? That's `warn`.
- What's ambient background music? That's `ambient`.

### Pack manifest format

`cmd/soundpacks/{slug}/pack.json`:
```json
{
  "name": "Display Name",
  "description": "Short description.",
  "author": "Original creators (sounds), babble (pack)",
  "version": "1.0.0",
  "categories": {
    "ambient":  { "files": ["bg.mp3"],           "loop": true,  "volume": 0.06 },
    "init":     { "files": ["start.mp3"],         "loop": false, "volume": 0.5  },
    "action":   { "files": ["hit1.mp3", "hit2.mp3"], "loop": false, "volume": 0.4  },
    "read":     { "files": ["blip.mp3"],          "loop": false, "volume": 0.3  },
    "write":    { "files": ["clang.mp3"],         "loop": false, "volume": 0.4  },
    "network":  { "files": ["whoosh.mp3"],        "loop": false, "volume": 0.4  },
    "success":  { "files": ["win.mp3"],           "loop": false, "volume": 0.5  },
    "warn":     { "files": ["alert.mp3"],         "loop": false, "volume": 0.5  },
    "error":    { "files": ["fail.mp3"],          "loop": false, "volume": 0.7  },
    "meta":     { "files": ["click.mp3"],         "loop": false, "volume": 0.4  }
  }
}
```

Multiple files in a category = random selection per event. Good for variety on high-frequency categories like `action`.

### Registry entry

Add to `packRegistry` in `cmd/packs.go`. Each entry maps destination filenames to download URLs. The install command handles both ZIP archives (`.zip` suffix) and direct downloads automatically.

## Development

```bash
go build ./...          # build
go test ./...           # test
go run . packs          # list installed packs
go run . packs install <slug>  # install a pack
go run .                # run babble (starts web UI + file watcher)
```

Installed packs live in `~/.config/babble/soundpacks/`. Embedded manifests in `cmd/soundpacks/` get copied there on install; audio files are downloaded from the registry URLs.
