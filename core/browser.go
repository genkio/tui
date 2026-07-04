package core

import (
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
)

func CopyOSC52(s string) error {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\a"
	// tmux doesn't forward an app's bare OSC52 to the outer terminal; wrap it in
	// DCS passthrough (needs allow-passthrough on) so tmux re-emits it.
	if os.Getenv("TMUX") != "" {
		seq = "\x1bPtmux;\x1b" + seq + "\x1b\\"
	}
	_, err := os.Stdout.WriteString(seq)
	return err
}

func OpenInBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		// the empty "" is start's window-title arg; without it a quoted URL is
		// mistaken for the title and nothing opens
		return exec.Command("cmd", "/c", "start", "", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}
