# Audio Experience Overhaul — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform babble from a cacophonous arcade into a pleasant background soundscape with proper rate-limiting, two-tier volume architecture, ambient crossfading, and ducking.

**Architecture:** All changes are in the browser-side audio engine (`cmd/web/js/audio.js`) and pack manifests (`cmd/soundpacks/*/pack.json`). No Go backend changes needed. The engine gets three new subsystems: cooldown gate, tier/ducking manager, and ambient crossfader. Pack manifests gain optional fields with backwards-compatible defaults.

**Tech Stack:** Web Audio API (AudioContext, GainNode, AudioBufferSourceNode), vanilla JS (no dependencies).

---

### Task 1: Cooldown Gate

Add per-category cooldown to prevent sound pile-up during burst activity.

**Files:**
- Modify: `cmd/web/js/audio.js` (the `play()` method and new `_shouldPlay()` helper)

**Step 1: Add cooldown state and defaults to the constructor**

In `audio.js`, add to the constructor after `this._sampleCache`:

```javascript
/** Per-category cooldown defaults in ms. */
this._defaultCooldowns = {
  read: 200, action: 150, write: 200, meta: 300,
  network: 500, success: 400, error: 500, warn: 500,
  init: 1000,
};

/** Timestamps of last played sound per category. @type {Map<string, number>} */
this._lastPlayedAt = new Map();
```

**Step 2: Add the cooldown gate method**

Add after the `play()` method:

```javascript
/**
 * Returns 'play', 'flurry', or 'skip' based on category cooldown.
 * @param {string} category
 * @param {object} catDef
 * @returns {'play'|'flurry'|'skip'}
 */
_cooldownDecision(category, catDef) {
  const now = performance.now();
  const cooldownMs = catDef.cooldownMs ?? this._defaultCooldowns[category] ?? 200;
  const last = this._lastPlayedAt.get(category) ?? 0;
  const elapsed = now - last;

  if (elapsed >= cooldownMs) {
    this._lastPlayedAt.set(category, now);
    return 'play';
  }

  // During cooldown: background tier gets flurry, notification tier skips.
  const tier = this._getTier(category, catDef);
  if (tier === 'background') {
    return 'flurry';
  }
  return 'skip';
}
```

**Step 3: Add tier helper**

```javascript
/** Default tier assignments. */
_defaultTiers = {
  ambient: 'background', read: 'background', action: 'background',
  write: 'background', meta: 'background', network: 'background',
  init: 'notification', success: 'notification',
  warn: 'notification', error: 'notification',
};

/**
 * Returns the tier for a category: 'background' or 'notification'.
 * @param {string} category
 * @param {object} catDef
 * @returns {'background'|'notification'}
 */
_getTier(category, catDef) {
  return catDef.tier ?? this._defaultTiers[category] ?? 'background';
}
```

Note: `_defaultTiers` should be a property declaration at the class level (or assigned in constructor). Since this is vanilla JS with no build step, add it in the constructor as `this._defaultTiers = { ... }`.

**Step 4: Wire cooldown into play()**

Replace the body of `play()` (after the mute check and before the ambient/idle logic) to use the cooldown gate:

```javascript
play(event) {
  if (!this.ctx || !this.pack) return;
  if (this.mutedSessions.has(event.session)) return;

  this._resumeAmbientIfNeeded();
  this._resetIdleTimer();

  const category = event.category;
  const catDef = this.pack.categories?.[category];
  if (!catDef) return;

  // Ambient is handled by the loop system, not the cooldown gate.
  if (catDef.loop && this.activeLoops.has(category)) return;

  const decision = this._cooldownDecision(category, catDef);
  if (decision === 'skip') return;

  if (this.pack.synth || catDef.synth) {
    this.playSynth(category, catDef, decision === 'flurry');
  } else if (catDef.files?.length) {
    this.playSample(category, catDef, decision === 'flurry');
  }
}
```

**Step 5: Add flurry support to playSample and playSynth**

In `playSample()`, add a `flurry` parameter (default false). When true, reduce volume to 30% and apply a random playbackRate shift:

```javascript
async playSample(category, catDef, flurry = false) {
  // ... existing fetch/decode logic unchanged ...

  const source = this.ctx.createBufferSource();
  source.buffer = buffer;
  source.loop = catDef.loop ?? false;

  // Flurry: quieter + random pitch shift (+/- 5 semitones)
  let vol = this.getCategoryVolume(category, catDef.volume);
  if (flurry) {
    vol *= 0.3;
    const semitones = (Math.random() - 0.5) * 10; // -5 to +5
    source.playbackRate.value = Math.pow(2, semitones / 12);
  }

  const gainNode = this.ctx.createGain();
  gainNode.gain.value = vol;

  source.connect(gainNode);
  gainNode.connect(this.masterGain);
  source.start(this.ctx.currentTime);

  if (catDef.loop) {
    this.activeLoops.set(category, { source, gain: gainNode });
  }
}
```

Similarly update `playSynth()` to accept and use `flurry` parameter — apply `vol *= 0.3` and shift `freq` by a random semitone offset when flurry is true.

**Step 6: Test manually**

Run: `go run . &` then open http://localhost:3333
Trigger rapid events (e.g. Glob a large directory) and verify:
- Sounds no longer pile up
- During bursts, you hear occasional quieter/pitched variants instead of silence or cacophony
- Notification sounds (success, error) still play cleanly without being skipped

**Step 7: Commit**

```bash
git add cmd/web/js/audio.js
git commit -m "feat(audio): add per-category cooldown gate with flurry mode"
```

---

### Task 2: Two-Tier Volume with Ducking

Add volume ceilings per tier and ducking (background quiets when notifications play).

**Files:**
- Modify: `cmd/web/js/audio.js`

**Step 1: Add tier gain nodes in init()**

After the `masterGain` creation in `init()`, create two intermediate GainNodes:

```javascript
init() {
  if (this.ctx) return;
  this.ctx = new (window.AudioContext || window.webkitAudioContext)();
  this.masterGain = this.ctx.createGain();
  this.masterGain.gain.value = 0.8;
  this.masterGain.connect(this.ctx.destination);

  // Tier gain buses — all sounds route through one of these before master.
  this.bgGain = this.ctx.createGain();
  this.bgGain.gain.value = 1.0;
  this.bgGain.connect(this.masterGain);

  this.notifGain = this.ctx.createGain();
  this.notifGain.gain.value = 1.0;
  this.notifGain.connect(this.masterGain);
}
```

**Step 2: Add volume ceiling constants**

In the constructor:

```javascript
this._bgMaxVolume = 0.30;
this._notifMaxVolume = 0.70;
```

**Step 3: Route sounds through tier buses**

In `playSample()` and `playSynth()`, instead of connecting `gainNode` to `this.masterGain`, connect to the appropriate tier bus:

```javascript
const tier = this._getTier(category, catDef);
const bus = tier === 'notification' ? this.notifGain : this.bgGain;

// Clamp volume to tier ceiling
const maxVol = tier === 'notification' ? this._notifMaxVolume : this._bgMaxVolume;
const clampedVol = Math.min(vol, maxVol);
gainNode.gain.value = clampedVol;

gainNode.connect(bus);
```

Apply same routing to `startLoop()` for synth-based ambient loops.

**Step 4: Add ducking**

Add a method that temporarily reduces bgGain when a notification fires:

```javascript
/**
 * Ducks the background tier briefly for a notification sound.
 * Fast attack (50ms), slow release (300ms after duration).
 * @param {number} durationEstimate - estimated sound duration in seconds
 */
_duckBackground(durationEstimate) {
  if (!this.ctx || !this.bgGain) return;
  const now = this.ctx.currentTime;
  const duckLevel = 0.6; // reduce to 60% of current

  this.bgGain.gain.setTargetAtTime(duckLevel, now, 0.02);        // fast attack ~50ms
  this.bgGain.gain.setTargetAtTime(1.0, now + durationEstimate, 0.15); // slow release ~300ms
}
```

Call `_duckBackground()` in `playSample`/`playSynth` when `tier === 'notification'`:

```javascript
if (tier === 'notification') {
  const duration = catDef.duration ?? (buffer ? buffer.duration : 0.5);
  this._duckBackground(duration);
}
```

**Step 5: Test manually**

- Verify background sounds (read, action) are noticeably quieter than before
- Trigger a success or error event and verify the background dips momentarily
- Verify ambient volume is very low and stable

**Step 6: Commit**

```bash
git add cmd/web/js/audio.js
git commit -m "feat(audio): add two-tier volume architecture with ducking"
```

---

### Task 3: Ambient Crossfade Loop

Replace the jarring `source.loop = true` with smooth double-buffer crossfading.

**Files:**
- Modify: `cmd/web/js/audio.js`

**Step 1: Add ambient crossfade state to constructor**

```javascript
/** Active ambient crossfade state. */
this._ambientState = null; // { bufferA, bufferB, currentSource, nextTimeout, url }
```

**Step 2: Create the crossfade ambient method**

Replace the ambient path in `playSample()` with a dedicated method. When `catDef.loop` is true and there are files, call `_startAmbientLoop(category, catDef)` instead of the normal playSample path:

```javascript
/**
 * Starts a crossfading ambient loop. Uses two alternating buffer sources
 * with overlapping fade-in/fade-out.
 */
async _startAmbientLoop(category, catDef) {
  if (!this.ctx || !catDef.files?.length) return;

  const file = catDef.files[Math.floor(Math.random() * catDef.files.length)];
  const url = `/sounds/${encodeURIComponent(this.packName)}/${file}`;
  const vol = this.getCategoryVolume(category, catDef.volume);
  const maxVol = Math.min(vol, 0.10); // ambient ceiling
  const crossfadeMs = catDef.crossfadeMs ?? 2000;
  const crossfadeSec = crossfadeMs / 1000;

  let buffer = this._sampleCache.get(url);
  if (!buffer) {
    try {
      const res = await fetch(url);
      if (!res.ok) return;
      const arrayBuffer = await res.arrayBuffer();
      buffer = await this.ctx.decodeAudioData(arrayBuffer);
      this._sampleCache.set(url, buffer);
    } catch (err) {
      console.warn(`BabbleAudio: failed to load ambient ${url}:`, err);
      return;
    }
  }

  // Stop any existing ambient.
  this._stopLoop('ambient');
  this.activeLoops.delete('ambient');

  const scheduleNext = () => {
    const now = this.ctx.currentTime;
    const source = this.ctx.createBufferSource();
    source.buffer = buffer;

    const gainNode = this.ctx.createGain();
    // Fade in
    gainNode.gain.setValueAtTime(0.001, now);
    gainNode.gain.linearRampToValueAtTime(maxVol, now + crossfadeSec);
    // Sustain, then fade out before end
    const sustainEnd = buffer.duration - crossfadeSec;
    if (sustainEnd > crossfadeSec) {
      gainNode.gain.setValueAtTime(maxVol, now + sustainEnd);
      gainNode.gain.linearRampToValueAtTime(0.001, now + buffer.duration);
    }

    source.connect(gainNode);
    gainNode.connect(this.bgGain);
    source.start(now);
    source.stop(now + buffer.duration + 0.05);

    // Track for stopAllLoops.
    this.activeLoops.set('ambient', { source, gain: gainNode });

    // Schedule the next iteration, overlapping by crossfadeSec.
    const nextDelay = Math.max((buffer.duration - crossfadeSec) * 1000, 1000);
    this._ambientState = {
      timeout: setTimeout(() => scheduleNext(), nextDelay),
      url,
    };
  };

  scheduleNext();
}
```

**Step 3: Wire into playSample and loadPack**

In `playSample()`, at the top, redirect ambient loops:
```javascript
if (catDef.loop) {
  this._startAmbientLoop(category, catDef);
  return;
}
```

In `_resumeAmbientIfNeeded()`, use the same path:
```javascript
_resumeAmbientIfNeeded() {
  if (this.activeLoops.has('ambient')) return;
  const catDef = this.pack?.categories?.ambient;
  if (!catDef?.loop) return;

  if (catDef.files?.length) {
    this._startAmbientLoop('ambient', catDef);
  } else {
    this.startLoop('ambient', catDef, this.getCategoryVolume('ambient', catDef.volume));
  }
}
```

**Step 4: Clean up ambient timeout on stop**

In `stopAllLoops()` and `_stopLoop('ambient')`, clear the ambient timeout:

```javascript
_stopLoop(category) {
  if (category === 'ambient' && this._ambientState?.timeout) {
    clearTimeout(this._ambientState.timeout);
    this._ambientState = null;
  }
  // ... existing stop logic ...
}
```

**Step 5: Improve idle fade-out ramp**

In `_resetIdleTimer`, change the ambient stop to use a 1-second fade:

```javascript
this._idleTimer = setTimeout(() => {
  const loop = this.activeLoops.get('ambient');
  if (loop && this.ctx) {
    loop.gain.gain.setTargetAtTime(0.001, this.ctx.currentTime, 0.3); // ~1s fade
    setTimeout(() => {
      this._stopLoop('ambient');
      this.activeLoops.delete('ambient');
    }, 1200);
  }
}, this.idleTimeoutMs);
```

**Step 6: Test manually**

- Load any sample-based pack (donkeykong, arcademix)
- Verify ambient loops smoothly without a hard restart seam
- Verify idle timeout produces a gentle fade-out
- Verify switching packs stops the old ambient cleanly

**Step 7: Commit**

```bash
git add cmd/web/js/audio.js
git commit -m "feat(audio): add ambient crossfade looping with smooth idle fade"
```

---

### Task 4: Improve Default Synth Pack

Make the default synth ambient a warmer, evolving drone instead of the current barely-audible breath.

**Files:**
- Modify: `cmd/soundpacks/default/pack.json`
- Modify: `cmd/web/js/audio.js` (improve the synth ambient generator)

**Step 1: Add an evolving drone synth type**

Add a new `'drone'` case in `playSynth()`:

```javascript
case 'drone': {
  // Warm evolving drone: two detuned triangle oscillators + slow LFO on volume.
  const osc1 = this.ctx.createOscillator();
  osc1.type = 'triangle';
  osc1.frequency.value = freq;

  const osc2 = this.ctx.createOscillator();
  osc2.type = 'triangle';
  osc2.frequency.value = freq * 1.005; // slight detune for warmth

  const gainNode = this.ctx.createGain();
  gainNode.gain.setValueAtTime(vol * 0.5, now);

  // Slow volume LFO for organic movement.
  const lfo = this.ctx.createOscillator();
  lfo.type = 'sine';
  lfo.frequency.value = 0.15; // very slow pulse
  const lfoGain = this.ctx.createGain();
  lfoGain.gain.value = vol * 0.3;
  lfo.connect(lfoGain);
  lfoGain.connect(gainNode.gain);
  lfo.start(now);

  osc1.connect(gainNode);
  osc2.connect(gainNode);
  gainNode.connect(this.bgGain);
  osc1.start(now);
  osc2.start(now);

  // For looping synth ambient, track it and don't auto-stop.
  if (catDef.loop) {
    this.activeLoops.set(category, { source: osc1, gain: gainNode, extras: [osc2, lfo, lfoGain] });
    return; // don't schedule stop
  }

  osc1.stop(now + duration + 0.01);
  osc2.stop(now + duration + 0.01);
  lfo.stop(now + duration + 0.01);
  break;
}
```

Update `_stopLoop` to handle the `extras` array:

```javascript
_stopLoop(category) {
  // ... ambient timeout cleanup ...
  const loop = this.activeLoops.get(category);
  if (!loop) return;
  try {
    loop.gain.gain.exponentialRampToValueAtTime(0.001, this.ctx.currentTime + 0.5);
    const stopTime = this.ctx.currentTime + 0.51;
    if (loop.source.stop) loop.source.stop(stopTime);
    if (loop.extras) {
      for (const node of loop.extras) {
        if (node.stop) node.stop(stopTime);
      }
    }
  } catch (_) {}
}
```

**Step 2: Update the default pack manifest**

```json
{
  "name": "Default",
  "description": "Built-in synthesized sounds",
  "author": "babble",
  "version": "1.1.0",
  "synth": true,
  "categories": {
    "ambient":  { "synth": "drone",  "freq": 110,  "duration": 0.5, "loop": true,  "volume": 0.08 },
    "action":   { "synth": "click",  "freq": 800,  "duration": 0.05, "loop": false, "volume": 0.25 },
    "read":     { "synth": "sine",   "freq": 440,  "duration": 0.08, "loop": false, "volume": 0.15 },
    "write":    { "synth": "sine",   "freq": 523,  "duration": 0.12, "loop": false, "volume": 0.25 },
    "network":  { "synth": "breath", "freq": 330,  "duration": 0.3,  "loop": false, "volume": 0.20 },
    "success":  { "synth": "chord",  "freq": 523,  "duration": 0.4,  "loop": false, "volume": 0.50 },
    "warn":     { "synth": "saw",    "freq": 330,  "duration": 0.3,  "loop": false, "volume": 0.50 },
    "error":    { "synth": "noise",  "freq": 200,  "duration": 0.3,  "loop": false, "volume": 0.60 },
    "meta":     { "synth": "click",  "freq": 660,  "duration": 0.04, "loop": false, "volume": 0.10 }
  }
}
```

Key changes from current: ambient becomes a warm drone at 110Hz, background-tier volumes pulled down (read 0.15, meta 0.10), notification tier volumes kept strong.

**Step 3: Test**

- Load the default pack, verify the ambient is a warm hum not an annoying blip
- Verify read/action/meta sounds are subtle background texture
- Verify success/error/warn cut through clearly

**Step 4: Commit**

```bash
git add cmd/web/js/audio.js cmd/soundpacks/default/pack.json
git commit -m "feat(audio): add drone synth type and rebalance default pack volumes"
```

---

### Task 5: Rebalance All Sample Pack Manifests

Update every pack manifest to use the two-tier volume scheme and add appropriate cooldown overrides.

**Files:**
- Modify: `cmd/soundpacks/arcademix/pack.json`
- Modify: `cmd/soundpacks/donkeykong/pack.json`
- Modify: `cmd/soundpacks/pacman/pack.json`
- Modify: `cmd/soundpacks/spaceinvaders/pack.json`
- Modify: `cmd/soundpacks/frogger/pack.json`
- Modify: `cmd/soundpacks/asteroids/pack.json`
- Modify: `cmd/soundpacks/mortalkombat/pack.json`

**Step 1: Apply consistent volume tiers to all packs**

For every pack, ensure:
- `ambient`: volume 0.04–0.08
- `read`, `meta`: volume 0.10–0.20
- `action`, `write`, `network`: volume 0.20–0.30
- `success`: volume 0.40–0.50
- `warn`, `init`: volume 0.40–0.55
- `error`: volume 0.50–0.65

These are pack manifest values — the engine's tier ceiling provides a hard cap on top of these.

**Step 2: Update each pack**

Example for Donkey Kong:
```json
{
  "name": "Donkey Kong",
  "description": "Original 1981 Donkey Kong arcade sounds. Jumpman codes!",
  "author": "Nintendo (sounds), babble (pack)",
  "version": "1.1.0",
  "categories": {
    "ambient":  { "files": ["bacmusic.wav"],  "loop": true,  "volume": 0.06, "crossfadeMs": 2000 },
    "action":   { "files": ["walking.wav"],   "loop": false, "volume": 0.25 },
    "read":     { "files": ["jump.wav"],      "loop": false, "volume": 0.15 },
    "write":    { "files": ["hammer.wav"],    "loop": false, "volume": 0.25 },
    "network":  { "files": ["jumpbar.wav"],   "loop": false, "volume": 0.20 },
    "success":  { "files": ["win1.wav", "win2.wav"], "loop": false, "volume": 0.50 },
    "warn":     { "files": ["howhigh.wav"],   "loop": false, "volume": 0.50 },
    "error":    { "files": ["death.wav"],     "loop": false, "volume": 0.60 },
    "meta":     { "files": ["itemget.wav"],   "loop": false, "volume": 0.10 }
  }
}
```

Apply similar rebalancing to all other packs following the tier volume guidelines. Add `crossfadeMs` to all ambient categories that have `loop: true`.

**Step 3: Test each pack**

Load each pack in the UI and verify:
- Ambient is pleasant background, not grating
- Activity sounds are subtle
- Notifications cut through
- No pack has any category that's painfully loud

**Step 4: Commit**

```bash
git add cmd/soundpacks/*/pack.json
git commit -m "feat(audio): rebalance all pack volumes for two-tier architecture"
```

---

### Task 6: Ambient Audio Sourcing & Replacement

Research and replace broken ambient files. This is the most manual task — some files may need trial and error.

**Files:**
- Modify: `cmd/packs.go` (add new download URLs if needed)
- Modify: pack manifests if ambient filenames change

**Step 1: Audit current ambient files**

For each installed pack, check the ambient file duration and quality:

```bash
for pack in ~/.config/babble/soundpacks/*/; do
  name=$(basename "$pack")
  ambient=$(jq -r '.categories.ambient.files[0] // "none"' "$pack/pack.json")
  if [ "$ambient" != "none" ] && [ -f "$pack/$ambient" ]; then
    duration=$(ffprobe -v quiet -show_entries format=duration -of csv=p=0 "$pack/$ambient" 2>/dev/null || echo "unknown")
    echo "$name: $ambient ($duration sec)"
  else
    echo "$name: $ambient (not installed or no file)"
  fi
done
```

**Step 2: Identify replacements needed**

Packs where the ambient is a short SFX (< 3 seconds) need replacement:
- `arcademix`: `smb_powerup.wav` — definitely needs replacement
- `asteroids`: `beat1.wav`/`beat2.wav` — short blips, need replacement
- `spaceinvaders`: `fastinvader1.wav` — single beat, need replacement
- `pacman`: `pacman-beginning.wav` — this is the intro jingle (~4s), might be OK if it's the right length, but likely too short for pleasant looping

Packs that might be OK:
- `donkeykong`: `bacmusic.wav` — actual background music, check length
- `frogger`: `frogger-music.mp3` — actual music, probably good
- `mortalkombat`: `mk1-music-cue1.mp3` — check length

**Step 3: Source new ambient files**

For packs needing replacement, look for longer ambient-suitable files from the same sound sources listed in CLAUDE.md. If none exist, consider:
- Using the synth engine as fallback (add a `"synth": "drone"` with pack-appropriate frequency)
- Finding alternative sources

This step requires human judgment — the implementer should listen to candidates and pick the best fit.

**Step 4: Update registry and manifests**

Add any new download URLs to `packRegistry` in `cmd/packs.go`. Update pack manifests to reference new ambient filenames.

**Step 5: Test**

Reinstall affected packs (`go run . packs install <slug>`) and verify the new ambient is pleasant to loop.

**Step 6: Commit**

```bash
git add cmd/packs.go cmd/soundpacks/*/pack.json
git commit -m "feat(audio): improve ambient audio selections across packs"
```

---

### Task 7: Integration Test & Polish

Final pass to verify everything works together.

**Files:**
- Modify: `cmd/web/js/audio.js` (any final fixes)

**Step 1: Full playthrough test**

1. `go build ./...` — verify it compiles
2. `go test ./...` — verify existing tests pass
3. Start babble: `go run .`
4. Open http://localhost:3333
5. Enable audio, select each pack one by one
6. For each pack verify:
   - [ ] Ambient plays smoothly without jarring seams
   - [ ] Rapid activity produces pleasant texture, not cacophony
   - [ ] Success/error/warn sounds are clearly audible over background
   - [ ] Switching packs cleanly stops old ambient
   - [ ] Idle timeout produces smooth fade-out
   - [ ] Volume sliders still work correctly
   - [ ] Session muting still works

**Step 2: Fix any issues discovered**

Address problems found during testing.

**Step 3: Final commit**

```bash
git add -A
git commit -m "fix(audio): polish and integration fixes for audio overhaul"
```
