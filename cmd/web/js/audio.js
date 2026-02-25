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
      this.startLoop('ambient', catDef, this.getCategoryVolume('ambient', catDef.volume));
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

    const category = event.category;
    const catDef = this.pack.categories?.[category];
    if (!catDef) return;

    if (this.pack.synth || catDef.synth) {
      // For looping categories the loop was already started; just let it run.
      if (!catDef.loop) {
        this.playSynth(category, catDef);
      }
    } else if (catDef.files?.length) {
      this.playSample(category, catDef);
    }
  }

  // ---------------------------------------------------------------------------
  // Synth engine
  // ---------------------------------------------------------------------------

  /**
   * Plays a synthesized sound for the given category definition.
   * Handles: sine, saw, click, chord, noise.
   * @param {string} category
   * @param {object} catDef
   */
  playSynth(category, catDef) {
    if (!this.ctx) return;

    const vol = this.getCategoryVolume(category, catDef.volume);
    const duration = catDef.duration ?? 0.1;
    const freq = catDef.freq ?? 440;
    const now = this.ctx.currentTime;

    switch (catDef.synth) {
      case 'sine':
        this._playSingleOscillator('sine', freq, duration, vol);
        break;

      case 'saw':
        this._playSingleOscillator('sawtooth', freq, duration, vol);
        break;

      case 'click':
        this._playSingleOscillator('square', freq, Math.min(duration, 0.06), vol);
        break;

      case 'chord': {
        // Three-note chord: root, major third (~1.25x), perfect fifth (~1.5x).
        const intervals = [1, 1.25, 1.5];
        for (const ratio of intervals) {
          this._playSingleOscillator('sine', freq * ratio, duration, vol / intervals.length);
        }
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
        gainNode.connect(this.masterGain);
        source.start(now);
        break;
      }

      default:
        // Unknown synth type — fall back to sine.
        this._playSingleOscillator('sine', freq, duration, vol);
    }
  }

  /**
   * Internal: creates a single oscillator with exponential gain ramp.
   * @param {OscillatorType} type
   * @param {number} freq
   * @param {number} duration
   * @param {number} vol
   */
  _playSingleOscillator(type, freq, duration, vol) {
    if (!this.ctx) return;
    const now = this.ctx.currentTime;

    const osc = this.ctx.createOscillator();
    osc.type = type;
    osc.frequency.value = freq;

    const gainNode = this.ctx.createGain();
    gainNode.gain.setValueAtTime(vol, now);
    gainNode.gain.exponentialRampToValueAtTime(0.001, now + duration);

    osc.connect(gainNode);
    gainNode.connect(this.masterGain);
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
   */
  async playSample(category, catDef) {
    if (!this.ctx || !catDef.files?.length) return;

    const file = catDef.files[Math.floor(Math.random() * catDef.files.length)];
    const url = `/sounds/${encodeURIComponent(this.packName)}/${file}`;
    const vol = this.getCategoryVolume(category, catDef.volume);

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

    const source = this.ctx.createBufferSource();
    source.buffer = buffer;
    source.loop = catDef.loop ?? false;

    const gainNode = this.ctx.createGain();
    gainNode.gain.value = vol;

    source.connect(gainNode);
    gainNode.connect(this.masterGain);
    source.start(this.ctx.currentTime);

    if (catDef.loop) {
      // Track looping sample sources so stopAllLoops() can stop them.
      this.activeLoops.set(category, { source, gain: gainNode });
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
    gainNode.connect(this.masterGain);
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
    const loop = this.activeLoops.get(category);
    if (!loop) return;
    try {
      loop.gain.gain.exponentialRampToValueAtTime(0.001, this.ctx.currentTime + 0.3);
      if (loop.source.stop) {
        loop.source.stop(this.ctx.currentTime + 0.31);
      }
    } catch (_) {
      // Already stopped — ignore.
    }
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
