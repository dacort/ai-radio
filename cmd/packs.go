package cmd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dacort/babble/internal/packs"
)

// remotePack describes a downloadable sound pack: its slug (directory name),
// display name, and a map of destination filenames to ZIP download URLs.
type remotePack struct {
	slug        string
	displayName string
	sounds      map[string]string
}

// packRegistry lists all packs available for download via `babble packs install`.
var packRegistry = []remotePack{
	{
		slug:        "donkeykong",
		displayName: "Donkey Kong",
		sounds: map[string]string{
			"walking.wav":  "https://www.classicgaming.cc/classics/donkey-kong/sound-files/walking.zip",
			"jump.wav":     "https://www.classicgaming.cc/classics/donkey-kong/sound-files/jump.zip",
			"jumpbar.wav":  "https://www.classicgaming.cc/classics/donkey-kong/sound-files/jumpbar.zip",
			"death.wav":    "https://www.classicgaming.cc/classics/donkey-kong/sound-files/death.zip",
			"hammer.wav":   "https://www.classicgaming.cc/classics/donkey-kong/sound-files/hammer.zip",
			"itemget.wav":  "https://www.classicgaming.cc/classics/donkey-kong/sound-files/itemget.zip",
			"howhigh.wav":  "https://www.classicgaming.cc/classics/donkey-kong/sound-files/howhigh.zip",
			"bacmusic.wav": "https://www.classicgaming.cc/classics/donkey-kong/sound-files/bacmusic.zip",
			"win1.wav":     "https://www.classicgaming.cc/classics/donkey-kong/sound-files/win1.zip",
			"win2.wav":     "https://www.classicgaming.cc/classics/donkey-kong/sound-files/win2.zip",
		},
	},
	{
		slug:        "pacman",
		displayName: "Pac-Man",
		sounds: map[string]string{
			"pacman-beginning.wav":    "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-beginning.zip",
			"pacman-chomp.wav":        "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-chomp.zip",
			"pacman-eatfruit.wav":     "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-eatfruit.zip",
			"pacman-eatghost.wav":     "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-eatghost.zip",
			"pacman-extrapac.wav":     "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-extrapac.zip",
			"pacman-intermission.wav": "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-intermission.zip",
			"pacman-death.wav":        "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-death.zip",
		},
	},
	{
		slug:        "spaceinvaders",
		displayName: "Space Invaders",
		sounds: map[string]string{
			"fastinvader1.wav":  "https://www.classicgaming.cc/classics/space-invaders/files/sounds/fastinvader1.zip",
			"fastinvader2.wav":  "https://www.classicgaming.cc/classics/space-invaders/files/sounds/fastinvader2.zip",
			"fastinvader3.wav":  "https://www.classicgaming.cc/classics/space-invaders/files/sounds/fastinvader3.zip",
			"fastinvader4.wav":  "https://www.classicgaming.cc/classics/space-invaders/files/sounds/fastinvader4.zip",
			"shoot.wav":         "https://www.classicgaming.cc/classics/space-invaders/files/sounds/shoot.zip",
			"invaderkilled.wav": "https://www.classicgaming.cc/classics/space-invaders/files/sounds/invaderkilled.zip",
			"explosion.wav":     "https://www.classicgaming.cc/classics/space-invaders/files/sounds/explosion.zip",
			"ufo_highpitch.wav": "https://www.classicgaming.cc/classics/space-invaders/files/sounds/ufo_highpitch.zip",
			"ufo_lowpitch.wav":  "https://www.classicgaming.cc/classics/space-invaders/files/sounds/ufo_lowpitch.zip",
		},
	},
	{
		slug:        "frogger",
		displayName: "Frogger",
		sounds: map[string]string{
			"frogger-music.mp3": "https://www.classicgaming.cc/classics/frogger/files/sounds/frogger-music.zip",
			"frogger-hop.wav":   "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-hop.zip",
			"frogger-coin.wav":  "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-coin-in.zip",
			"frogger-extra.wav": "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-extra.zip",
			"frogger-plunk.wav": "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-plunk.zip",
			"frogger-squash.wav": "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-squash.zip",
			"frogger-time.wav":  "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-time.zip",
		},
	},
	{
		slug:        "asteroids",
		displayName: "Asteroids",
		sounds: map[string]string{
			"beat1.wav":       "https://www.classicgaming.cc/classics/asteroids/files/sounds/beat1.zip",
			"beat2.wav":       "https://www.classicgaming.cc/classics/asteroids/files/sounds/beat2.zip",
			"fire.wav":        "https://www.classicgaming.cc/classics/asteroids/files/sounds/fire.zip",
			"thrust.wav":      "https://www.classicgaming.cc/classics/asteroids/files/sounds/thrust.zip",
			"saucersmall.wav": "https://www.classicgaming.cc/classics/asteroids/files/sounds/saucersmall.zip",
			"saucerbig.wav":   "https://www.classicgaming.cc/classics/asteroids/files/sounds/saucerbig.zip",
			"bangsmall.wav":   "https://www.classicgaming.cc/classics/asteroids/files/sounds/bangsmall.zip",
			"bangmedium.wav":  "https://www.classicgaming.cc/classics/asteroids/files/sounds/bangmedium.zip",
			"banglarge.wav":   "https://www.classicgaming.cc/classics/asteroids/files/sounds/banglarge.zip",
			"extraship.wav":   "https://www.classicgaming.cc/classics/asteroids/files/sounds/extraship.zip",
		},
	},
	{
		slug:        "arcademix",
		displayName: "Arcade Mix",
		sounds: map[string]string{
			// Mario (themushroomkingdom.net — direct WAV downloads)
			"smb_powerup.wav":     "https://themushroomkingdom.net/sounds/wav/smb/smb_powerup.wav",
			"smb_stage_clear.wav": "https://themushroomkingdom.net/sounds/wav/smb/smb_stage_clear.wav",
			"smb_coin.wav":        "https://themushroomkingdom.net/sounds/wav/smb/smb_coin.wav",
			"smb_mariodie.wav":    "https://themushroomkingdom.net/sounds/wav/smb/smb_mariodie.wav",
			"smb_warning.wav":     "https://themushroomkingdom.net/sounds/wav/smb/smb_warning.wav",
			"smb_breakblock.wav":  "https://themushroomkingdom.net/sounds/wav/smb/smb_breakblock.wav",
			// Pac-Man chomp (classicgaming.cc — ZIP)
			"pacman-chomp.wav": "https://www.classicgaming.cc/classics/pac-man/files/sounds/pacman-chomp.zip",
			// Space Invaders laser (classicgaming.cc — ZIP)
			"shoot.wav": "https://www.classicgaming.cc/classics/space-invaders/files/sounds/shoot.zip",
			// Frogger hop (classicgaming.cc — ZIP)
			"frogger-hop.wav": "https://www.classicgaming.cc/classics/frogger/files/sounds/sound-frogger-hop.zip",
			// Donkey Kong boing (classicgaming.cc — ZIP)
			"jump.wav": "https://www.classicgaming.cc/classics/donkey-kong/sound-files/jump.zip",
			// Zelda (noproblo.dayjo.org — direct WAV downloads)
			"loz_get_item.wav": "https://noproblo.dayjo.org/zeldasounds/LOZ/LOZ_Get_Item.wav",
			"loz_secret.wav":   "https://noproblo.dayjo.org/zeldasounds/LOZ/LOZ_Secret.wav",
		},
	},
	{
		slug:        "mortalkombat",
		displayName: "Mortal Kombat",
		sounds: map[string]string{
			// Announcer (mortalkombatwarehouse.com — direct MP3)
			"mk1-fight.mp3":         "https://www.mortalkombatwarehouse.com/mk1/sounds/announcer/mk1-00368.mp3",
			"mk1-fatality.mp3":      "https://www.mortalkombatwarehouse.com/mk1/sounds/announcer/mk1-00375.mp3",
			"mk1-flawless.mp3":      "https://www.mortalkombatwarehouse.com/mk1/sounds/announcer/mk1-00376.mp3",
			"mk1-excellent.mp3":     "https://www.mortalkombatwarehouse.com/mk1/sounds/announcer/mk1-00377.mp3",
			"mk1-finishhim.mp3":     "https://www.mortalkombatwarehouse.com/mk1/sounds/announcer/mk1-00378.mp3",
			"mk1-testyourmight.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/announcer/mk1-00381.mp3",
			// Hit sounds
			"mk1-hit1.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/hitsounds/mk1-00048.mp3",
			"mk1-hit2.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/hitsounds/mk1-00049.mp3",
			"mk1-hit3.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/hitsounds/mk1-00050.mp3",
			"mk1-hit4.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/hitsounds/mk1-00051.mp3",
			// Special FX
			"mk1-spear.mp3":       "https://www.mortalkombatwarehouse.com/mk1/sounds/specialfx/mk1-00151.mp3",
			"mk1-getoverhere.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/scorpion/mk1-goh.mp3",
			// Explosion
			"mk1-explosion.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/explosions/mk1-00085.mp3",
			// Music cue (ambient loop)
			"mk1-music-cue1.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/musiccues/mk1-00016.mp3",
			// UI sounds
			"mk1-insertcoin.mp3": "https://www.mortalkombatwarehouse.com/mk1/sounds/ui/mk1-00168.mp3",
			"mk1-ui1.mp3":        "https://www.mortalkombatwarehouse.com/mk1/sounds/ui/mk1-00163.mp3",
			"mk1-ui2.mp3":        "https://www.mortalkombatwarehouse.com/mk1/sounds/ui/mk1-00164.mp3",
		},
	},
}

func runPacks(args []string) error {
	home, _ := os.UserHomeDir()
	packsDir := filepath.Join(home, ".config", "babble", "soundpacks")

	if len(args) == 0 {
		return listPacks(packsDir)
	}

	switch args[0] {
	case "install":
		if len(args) < 2 {
			fmt.Println("Usage: babble packs install <name>")
			fmt.Println("\nAvailable packs:")
			for _, rp := range packRegistry {
				fmt.Printf("  %-16s %s\n", rp.slug, rp.displayName)
			}
			return nil
		}
		return installPack(args[1], packsDir)
	default:
		return listPacks(packsDir)
	}
}

func listPacks(packsDir string) error {
	packList, err := packs.ListPacks(packsDir)
	if err != nil {
		fmt.Println("No sound packs installed yet.")
		return nil
	}
	if len(packList) == 0 {
		fmt.Println("No sound packs installed yet.")
		return nil
	}
	fmt.Println("Installed sound packs:")
	for _, p := range packList {
		fmt.Printf("  %-15s %s\n", p.Name, p.Description)
	}

	fmt.Println("\nAvailable for install:")
	for _, rp := range packRegistry {
		installed := false
		for _, p := range packList {
			if p.Slug == rp.slug {
				installed = true
				break
			}
		}
		if !installed {
			fmt.Printf("  %-16s %s\n", rp.slug, rp.displayName)
		}
	}
	return nil
}

func installPack(name, packsDir string) error {
	for _, rp := range packRegistry {
		if rp.slug == name {
			return installRemotePack(rp, packsDir)
		}
	}
	available := make([]string, len(packRegistry))
	for i, rp := range packRegistry {
		available[i] = rp.slug
	}
	return fmt.Errorf("unknown pack: %s (available: %s)", name, strings.Join(available, ", "))
}

func installRemotePack(rp remotePack, packsDir string) error {
	packDir := filepath.Join(packsDir, rp.slug)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return fmt.Errorf("creating pack directory: %w", err)
	}

	// Copy the manifest from embedded FS.
	manifestPath := "soundpacks/" + rp.slug + "/pack.json"
	manifestData, err := defaultPacksFS.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading embedded manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), manifestData, 0o644); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}

	fmt.Printf("Installing %s sound pack...\n", rp.displayName)
	fmt.Printf("Downloading %d sounds from classicgaming.cc\n", len(rp.sounds))

	// Sort keys for deterministic output order.
	keys := make([]string, 0, len(rp.sounds))
	for k := range rp.sounds {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, destName := range keys {
		url := rp.sounds[destName]
		destPath := filepath.Join(packDir, destName)

		if _, err := os.Stat(destPath); err == nil {
			fmt.Printf("  [skip] %s (already exists)\n", destName)
			continue
		}

		fmt.Printf("  [download] %s ... ", destName)
		var dlErr error
		if strings.HasSuffix(url, ".zip") {
			dlErr = downloadAndExtractWav(url, destPath)
		} else {
			dlErr = downloadDirect(url, destPath)
		}
		if dlErr != nil {
			fmt.Printf("FAILED: %v\n", dlErr)
			continue
		}
		fmt.Println("ok")
	}

	fmt.Printf("\n%s pack installed! Select it in the Babble UI or set:\n", rp.displayName)
	fmt.Printf("  \"activePack\": \"%s\" in ~/.config/babble/config.json\n", rp.slug)
	return nil
}

// downloadAndExtractWav downloads a ZIP file, finds the first .wav inside,
// and writes it to destPath.
func downloadAndExtractWav(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("opening zip: %w", err)
	}

	for _, f := range reader.File {
		lower := strings.ToLower(f.Name)
		if strings.HasSuffix(lower, ".wav") || strings.HasSuffix(lower, ".mp3") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			if _, err := io.Copy(out, rc); err != nil {
				return err
			}
			return nil
		}
	}

	return fmt.Errorf("no .wav file found in zip")
}

// downloadDirect downloads a file directly (no ZIP) and writes it to destPath.
func downloadDirect(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
