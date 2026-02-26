/**
 * audio.js — BabbleAudio: Web Audio API engine for Babble sonification.
 *
 * Usage:
 *   import { BabbleAudio } from './audio.js';
 *   const audio = new BabbleAudio();
 *   await audio.init();
 *   await audio.loadPack('default');
 *   audio.play(wsEvent);
 */

export class BabbleAudio {
  constructor() {
    /** @type {AudioContext|null} */
    this.ctx = null;
    /** @type {GainNode|null} */
    this.masterGain = null;

    /** Currently loaded pack manifest (from /api/packs/{name}/manifest). */
    this.pack = null;
    /** Name of the currently loaded pack. */
    this.packName = null;

    /**
     * Per-category volume overrides. Keys are category strings, values 0–1.
     * When absent, the pack's category volume is used.
     * @type {Map<string, number>}
     */
    this.categoryOverrides = new Map();

    /**
     * Active looping oscillator nodes, keyed by category name.
     * @type {Map<string, {source: AudioNode, gain: GainNode}>}
     */
    this.activeLoops = new Map();

    /**
     * Set of session names that are muted.
     * @type {Set<string>}
     */
    this.mutedSessions = new Set();

    // Decoded audio buffer cache for sample-based packs.
    this._sampleCache = new Map();

    /** Per-category cooldown defaults in ms. */
    this._defaultCooldowns = {
      read: 200, action: 150, write: 200, meta: 300,
      network: 500, success: 400, error: 500, warn: 500,
      init: 1000,
    };

    /** Timestamps of last played sound per category. @type {Map<string, number>} */
    this._lastPlayedAt = new Map();

    /** Default tier assignments. */
    this._defaultTiers = {
      ambient: 'background', read: 'background', action: 'background',
      write: 'background', meta: 'background', network: 'background',
      init: 'notification', success: 'notification',
      warn: 'notification', error: 'notification',
    };

    /** Volume ceiling for background-tier sounds. */
    this._bgMaxVolume = 0.30;
    /** Volume ceiling for notification-tier sounds. */
    this._notifMaxVolume = 0.70;

    /** Active ambient crossfade state. */
    this._ambientState = null;

    /** Idle timeout in ms — ambient loops stop after this much silence. */
    this.idleTimeoutMs = 30_000;
    /** @type {number|null} Timer ID for the idle check. */
    this._idleTimer = null;
  }

  // ---------------------------------------------------------------------------
  // Lifecycle
  // ---------------------------------------------------------------------------

  /**
   * Creates the AudioContext and master gain node.
   * Must be called from a user-gesture handler to satisfy autoplay policy.
   */
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

  /**
   * Fetches the manifest for packName and replaces the active pack.
   * Stops all active loops before switching.
   * @param {string} packName
   */
  async loadPack(packName) {
    this.stopAllLoops();
    this._sampleCache.clear();

    const res = await fetch(`/api/packs/${encodeURIComponent(packName)}/manifest`);
    if (!res.ok) throw new Error(`Failed to load pack "${packName}": ${res.status}`);
    this.pack = await res.json();
    this.packName = packName;

    // Pre-start ambient loop if defined.
    if (this.ctx && this.pack.categories?.ambient?.loop) {
      const catDef = this.pack.categories.ambient;
      if (catDef.files?.length) {
        this._startAmbientLoop('ambient', catDef);
      } else if (catDef.synth) {
        this.playSynth('ambient', catDef);
      } else {
        this.startLoop('ambient', catDef, this.getCategoryVolume('ambient', catDef.volume));
      }
    }
  }

  // ---------------------------------------------------------------------------
  // Playback dispatch
  // ---------------------------------------------------------------------------

  /**
   * Main entry point: receives a WebSocket event object and plays the
   * appropriate sound if the session is not muted and the pack is loaded.
   * @param {{ session: string, category: string, event: string, detail: string, timestamp: string }} event
   */
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

  /**
   * Returns the tier for a category: 'background' or 'notification'.
   * @param {string} category
   * @param {object} catDef
   * @returns {'background'|'notification'}
   */
  _getTier(category, catDef) {
    return catDef.tier ?? this._defaultTiers[category] ?? 'background';
  }

  /**
   * Ducks the background tier briefly for a notification sound.
   * @param {number} durationEstimate - estimated sound duration in seconds
   */
  _duckBackground(durationEstimate) {
    if (!this.ctx || !this.bgGain) return;
    const now = this.ctx.currentTime;
    const duckLevel = 0.6;
    this.bgGain.gain.cancelScheduledValues(now);
    this.bgGain.gain.setTargetAtTime(duckLevel, now, 0.02);
    this.bgGain.gain.setTargetAtTime(1.0, now + durationEstimate, 0.15);
  }

  /**
   * Sets the idle timeout in milliseconds. The ambient loop is stopped after
   * this duration of inactivity and restarted when the next event arrives.
   * @param {number} ms
   */
  setIdleTimeout(ms) {
    this.idleTimeoutMs = ms;
    this._resetIdleTimer();
  }

  /** Resets the idle timer. Called on every incoming event. */
  _resetIdleTimer() {
    if (this._idleTimer) clearTimeout(this._idleTimer);
    if (this.idleTimeoutMs <= 0) return;
    this._idleTimer = setTimeout(() => {
      const loop = this.activeLoops.get('ambient');
      if (loop && this.ctx) {
        loop.gain.gain.setTargetAtTime(0.001, this.ctx.currentTime, 0.3);
        setTimeout(() => {
          this._stopLoop('ambient');
          this.activeLoops.delete('ambient');
        }, 1200);
      }
    }, this.idleTimeoutMs);
  }

  /** Restarts the ambient loop if the pack defines one and it's not running. */
  _resumeAmbientIfNeeded() {
    if (this.activeLoops.has('ambient')) return;
    const catDef = this.pack?.categories?.ambient;
    if (!catDef?.loop) return;

    if (catDef.files?.length) {
      this._startAmbientLoop('ambient', catDef);
    } else if (catDef.synth) {
      this.playSynth('ambient', catDef);
    } else {
      this.startLoop('ambient', catDef, this.getCategoryVolume('ambient', catDef.volume));
    }
  }

  /**
   * Starts a crossfading ambient loop. Uses two alternating buffer sources
   * with overlapping fade-in/fade-out for seamless looping.
   * @param {string} category
   * @param {object} catDef
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
      gainNode.connect(this.bgGain ?? this.masterGain);
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

  // ---------------------------------------------------------------------------
  // Synth engine
  // ---------------------------------------------------------------------------

  /**
   * Plays a synthesized sound for the given category definition.
   * Handles: sine, saw, click, chord, noise.
   * @param {string} category
   * @param {object} catDef
   * @param {boolean} flurry  When true, reduces volume and randomises pitch.
   */
  playSynth(category, catDef, flurry = false) {
    if (!this.ctx) return;

    let vol = this.getCategoryVolume(category, catDef.volume);
    const duration = catDef.duration ?? 0.1;
    let freq = catDef.freq ?? 440;

    if (flurry) {
      vol *= 0.3;
      freq *= Math.pow(2, ((Math.random() - 0.5) * 10) / 12);
    }

    const tier = this._getTier(category, catDef);
    const bus = tier === 'notification' ? this.notifGain : this.bgGain;
    const maxVol = tier === 'notification' ? this._notifMaxVolume : this._bgMaxVolume;
    vol = Math.min(vol, maxVol);

    if (tier === 'notification') {
      this._duckBackground(catDef.duration ?? 0.5);
    }

    const now = this.ctx.currentTime;

    switch (catDef.synth) {
      case 'sine':
        this._playSingleOscillator('sine', freq, duration, vol, bus);
        break;

      case 'saw':
        this._playSingleOscillator('sawtooth', freq, duration, vol, bus);
        break;

      case 'click':
        this._playSingleOscillator('square', freq, Math.min(duration, 0.06), vol, bus);
        break;

      case 'chord': {
        // Three-note chord: root, major third (~1.25x), perfect fifth (~1.5x).
        const intervals = [1, 1.25, 1.5];
        for (const ratio of intervals) {
          this._playSingleOscillator('sine', freq * ratio, duration, vol / intervals.length, bus);
        }
        break;
      }

      case 'breath': {
        // Soft fade-in / fade-out "whoosh" — gentle ambient pulse.
        const attackTime = duration * 0.4;
        const releaseTime = duration * 0.6;
        const osc = this.ctx.createOscillator();
        osc.type = 'triangle';
        osc.frequency.value = freq;

        const gainNode = this.ctx.createGain();
        gainNode.gain.setValueAtTime(0.001, now);
        gainNode.gain.linearRampToValueAtTime(vol, now + attackTime);
        gainNode.gain.exponentialRampToValueAtTime(0.001, now + duration);

        osc.connect(gainNode);
        gainNode.connect(bus);
        osc.start(now);
        osc.stop(now + duration + 0.01);
        break;
      }

      case 'noise': {
        const bufferSize = Math.ceil(this.ctx.sampleRate * duration);
        const buffer = this.ctx.createBuffer(1, bufferSize, this.ctx.sampleRate);
        const data = buffer.getChannelData(0);
        for (let i = 0; i < bufferSize; i++) {
          data[i] = Math.random() * 2 - 1;
        }
        const source = this.ctx.createBufferSource();
        source.buffer = buffer;

        const gainNode = this.ctx.createGain();
        gainNode.gain.setValueAtTime(vol, now);
        gainNode.gain.exponentialRampToValueAtTime(0.001, now + duration);

        source.connect(gainNode);
        gainNode.connect(bus);
        source.start(now);
        break;
      }

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
        gainNode.connect(bus);
        osc1.start(now);
        osc2.start(now);

        // For looping synth ambient, track it and don't auto-stop.
        if (catDef.loop) {
          this.activeLoops.set(category, { source: osc1, gain: gainNode, extras: [osc2, lfo, lfoGain] });
          return;
        }

        osc1.stop(now + duration + 0.01);
        osc2.stop(now + duration + 0.01);
        lfo.stop(now + duration + 0.01);
        break;
      }

      default:
        // Unknown synth type — fall back to sine.
        this._playSingleOscillator('sine', freq, duration, vol, bus);
    }
  }

  /**
   * Internal: creates a single oscillator with exponential gain ramp.
   * @param {OscillatorType} type
   * @param {number} freq
   * @param {number} duration
   * @param {number} vol
   * @param {AudioNode} [bus] - destination node; defaults to masterGain
   */
  _playSingleOscillator(type, freq, duration, vol, bus) {
    if (!this.ctx) return;
    const now = this.ctx.currentTime;
    const destination = bus ?? this.masterGain;

    const osc = this.ctx.createOscillator();
    osc.type = type;
    osc.frequency.value = freq;

    const gainNode = this.ctx.createGain();
    gainNode.gain.setValueAtTime(vol, now);
    gainNode.gain.exponentialRampToValueAtTime(0.001, now + duration);

    osc.connect(gainNode);
    gainNode.connect(destination);
    osc.start(now);
    osc.stop(now + duration + 0.01);
  }

  // ---------------------------------------------------------------------------
  // Sample engine
  // ---------------------------------------------------------------------------

  /**
   * Picks a random file from catDef.files, fetches and decodes it (with
   * caching), then plays it via a buffer source node.
   * @param {string} category
   * @param {object} catDef
   * @param {boolean} flurry  When true, reduces volume and randomises playback rate.
   */
  async playSample(category, catDef, flurry = false) {
    if (!this.ctx || !catDef.files?.length) return;

    const file = catDef.files[Math.floor(Math.random() * catDef.files.length)];
    const url = `/sounds/${encodeURIComponent(this.packName)}/${file}`;
    let vol = this.getCategoryVolume(category, catDef.volume);

    let buffer = this._sampleCache.get(url);
    if (!buffer) {
      try {
        const res = await fetch(url);
        if (!res.ok) return;
        const arrayBuffer = await res.arrayBuffer();
        buffer = await this.ctx.decodeAudioData(arrayBuffer);
        this._sampleCache.set(url, buffer);
      } catch (err) {
        console.warn(`BabbleAudio: failed to load sample ${url}:`, err);
        return;
      }
    }

    if (catDef.loop) {
      this._startAmbientLoop(category, catDef);
      return;
    }

    const source = this.ctx.createBufferSource();
    source.buffer = buffer;

    if (flurry) {
      vol *= 0.3;
      const semitones = (Math.random() - 0.5) * 10;
      source.playbackRate.value = Math.pow(2, semitones / 12);
    }

    const tier = this._getTier(category, catDef);
    const bus = tier === 'notification' ? this.notifGain : this.bgGain;
    const maxVol = tier === 'notification' ? this._notifMaxVolume : this._bgMaxVolume;
    const gainNode = this.ctx.createGain();
    gainNode.gain.value = Math.min(vol, maxVol);
    source.connect(gainNode);
    gainNode.connect(bus);
    source.start(this.ctx.currentTime);

    if (tier === 'notification') {
      this._duckBackground(buffer.duration ?? 0.5);
    }
  }

  // ---------------------------------------------------------------------------
  // Loop management
  // ---------------------------------------------------------------------------

  /**
   * Starts a persistent looping oscillator for the given category.
   * Replaces any existing loop for that category.
   * @param {string} category
   * @param {object} catDef
   * @param {number} vol
   */
  startLoop(category, catDef, vol) {
    if (!this.ctx) return;

    // Stop any existing loop for this category.
    this._stopLoop(category);

    const freq = catDef.freq ?? 220;
    const now = this.ctx.currentTime;

    const osc = this.ctx.createOscillator();
    osc.type = catDef.synth === 'saw' ? 'sawtooth' : 'sine';
    osc.frequency.value = freq;

    const gainNode = this.ctx.createGain();
    gainNode.gain.setValueAtTime(vol, now);

    osc.connect(gainNode);
    gainNode.connect(this.bgGain ?? this.masterGain);
    osc.start(now);

    this.activeLoops.set(category, { source: osc, gain: gainNode });
  }

  /**
   * Stops all active loops (called when switching packs or on cleanup).
   */
  stopAllLoops() {
    for (const category of this.activeLoops.keys()) {
      this._stopLoop(category);
    }
    this.activeLoops.clear();
  }

  /**
   * Stops a single named loop.
   * @param {string} category
   */
  _stopLoop(category) {
    if (category === 'ambient' && this._ambientState?.timeout) {
      clearTimeout(this._ambientState.timeout);
      this._ambientState = null;
    }
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

  // ---------------------------------------------------------------------------
  // Volume control
  // ---------------------------------------------------------------------------

  /**
   * Returns the effective volume for a category: override if set, else packDefault.
   * @param {string} category
   * @param {number} packDefault
   * @returns {number}
   */
  getCategoryVolume(category, packDefault) {
    if (this.categoryOverrides.has(category)) {
      return this.categoryOverrides.get(category);
    }
    return packDefault ?? 0.5;
  }

  /**
   * Sets a per-category volume override and updates any live loop gain.
   * @param {string} category
   * @param {number} vol  0–1
   */
  setCategoryVolume(category, vol) {
    this.categoryOverrides.set(category, vol);

    // Update live loop gain if one is running for this category.
    const loop = this.activeLoops.get(category);
    if (loop && this.ctx) {
      loop.gain.gain.setTargetAtTime(vol, this.ctx.currentTime, 0.05);
    }
  }

  /**
   * Sets the master output gain.
   * @param {number} vol  0–1
   */
  setMasterVolume(vol) {
    if (this.masterGain && this.ctx) {
      this.masterGain.gain.setTargetAtTime(vol, this.ctx.currentTime, 0.05);
    }
  }

  // ---------------------------------------------------------------------------
  // Session muting
  // ---------------------------------------------------------------------------

  /**
   * Toggles mute for the given session name.
   * Returns true if the session is now muted, false if unmuted.
   * @param {string} session
   * @returns {boolean}
   */
  toggleSessionMute(session) {
    if (this.mutedSessions.has(session)) {
      this.mutedSessions.delete(session);
      return false;
    }
    this.mutedSessions.add(session);
    return true;
  }
}
