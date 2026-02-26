# Audio Experience Overhaul

**Date:** 2026-02-26
**Status:** Implemented

## Problem

The current audio experience has several issues that make it unpleasant to listen to for more than a minute or two:

1. **Terrible ambient loops** — Short game SFX (power-ups, beats, music stings) were never meant to loop. A 0.5-second sound repeating endlessly is grating.
2. **Sound overlap / pile-up** — No rate-limiting. Every event fires `playSample()` immediately. During burst activity, sounds stack creating cacophony.
3. **No cooldown or debouncing** — `read` events can fire 10+ times per second during Glob/Grep bursts.
4. **All sounds are foreground** — No distinction between background texture and notification-worthy events.

## Design

### 1. Cooldown + Flurry Engine

Per-category cooldown gate in `play()`:

- Check `lastPlayedAt[category]` against `now`
- Within cooldown: skip, or play "flurry" variant (30% volume, random pitch shift +/- 5 semitones)
- Past cooldown: play normally, reset timer

Default cooldowns (overridable per-category via `cooldownMs` in manifest):

| Category | Cooldown | Rationale |
|----------|----------|-----------|
| read     | 200ms    | Highest frequency, aggressive debounce |
| action   | 150ms    | Bash commands can burst during scripts |
| write    | 200ms    | Multiple edits in quick succession |
| meta     | 300ms    | System plumbing, keep subtle |
| network  | 500ms    | Rare, let each ring out |
| success  | 400ms    | Don't stack victory sounds |
| error    | 500ms    | Each error should be distinct |
| warn     | 500ms    | Attention events need space |
| init     | 1000ms   | Once per session, protect it |
| ambient  | N/A      | Handled by loop system |

Flurry behavior is only for background tier categories.

### 2. Two-Tier Volume Architecture

**Background tier:** `ambient`, `read`, `action`, `write`, `meta`, `network`
**Notification tier:** `init`, `success`, `warn`, `error`

**Volume ceiling per tier.** Background capped at 0.3 effective volume. Notification gets full range to 1.0.

**Ducking.** When a notification-tier sound fires, background-tier gain reduces ~40% (fast attack ~50ms, slow release ~300ms).

Volume targets:

| Tier         | Volume Range | Duck Amount |
|--------------|-------------|-------------|
| ambient      | 0.04 - 0.10 | ducked 60% |
| background   | 0.08 - 0.30 | ducked 40% |
| notification | 0.30 - 0.70 | not ducked  |

Tier assignments are hardcoded defaults, overridable per-category via `"tier"` in manifest.

### 3. Ambient Crossfade Loop

Double-buffer crossfade for sample-based ambient. Two AudioBufferSourceNodes — when buffer A approaches its end, buffer B starts with fade-in while A fades out. Overlap configurable via `crossfadeMs` (default: 2000ms).

Idle fade-out becomes a smooth 1-second ramp (currently abrupt 300ms).

Synth ambient (default pack) doesn't need crossfade.

### 4. Pack Manifest Changes

Optional new fields, all backwards-compatible:

```json
{
  "categories": {
    "read": {
      "files": ["coin.wav"],
      "volume": 0.3,
      "cooldownMs": 200,
      "tier": "background"
    },
    "ambient": {
      "files": ["ambient-loop.mp3"],
      "loop": true,
      "volume": 0.06,
      "crossfadeMs": 2000
    }
  }
}
```

| Field         | Default                    | Notes                          |
|---------------|----------------------------|--------------------------------|
| `cooldownMs`  | per-category defaults      | 0 = no cooldown                |
| `tier`        | hardcoded per category     | `"background"` or `"notification"` |
| `crossfadeMs` | 2000                       | Only for looping ambient       |

### 5. New Ambient Audio Per Pack

Replace broken short SFX with proper ambient tracks:

| Pack          | Current                      | Replacement goal                                    |
|---------------|------------------------------|-----------------------------------------------------|
| arcademix     | `smb_powerup.wav` (~1s)      | Longer mixed arcade ambience or filtered Mario theme |
| donkeykong    | `bacmusic.wav`               | Keep if long enough; verify length                   |
| asteroids     | `beat1.wav`/`beat2.wav`      | Deep space drone or properly-spaced heartbeat        |
| pacman        | (check)                      | Pac-Man siren/background wail                        |
| mortalkombat  | `mk1-music-cue1.mp3`        | MK fight music loop or crowd ambience                |
| frogger       | (check)                      | Frogger main theme snippet                           |
| spaceinvaders | (check)                      | Classic descending march rhythm, properly looped     |
| default       | synth "breath"               | Richer synth pad — warmer, evolving drone            |
