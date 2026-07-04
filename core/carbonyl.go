//go:build !windows

// Package core holds the TUI machinery every plugin reuses: the carbonyl pty
// runner, clipboard/browser helpers, the normalized feed item and widget, the
// theme, and the plugin Source interface.
package core

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
)

// CarbonylCmd runs carbonyl on a pty so a bare q quits back to the list and a
// bare b quits then opens the URL in the browser; carbonyl itself only exits on
// ctrl+c and would otherwise send those keys to the page. It implements
// bubbletea's ExecCommand, so callers wrap it in tea.Exec.
type CarbonylCmd struct {
	url         string
	graphics    bool
	openBrowser bool
	stdin       io.Reader
	stdout      io.Writer
}

func Carbonyl(url string, graphics bool) *CarbonylCmd {
	return &CarbonylCmd{url: url, graphics: graphics}
}

// OpenBrowser reports whether the user pressed b (quit and open in browser)
// rather than a plain q. Only meaningful once Run has returned.
func (c *CarbonylCmd) OpenBrowser() bool { return c.openBrowser }

func (c *CarbonylCmd) URL() string { return c.url }

func (c *CarbonylCmd) SetStdin(r io.Reader)  { c.stdin = r }
func (c *CarbonylCmd) SetStdout(w io.Writer) { c.stdout = w }
func (c *CarbonylCmd) SetStderr(io.Writer)   {}

func (c *CarbonylCmd) Run() error {
	bin, err := exec.LookPath("carbonyl")
	if err != nil {
		return errors.New("carbonyl not found; brew install genkio/tap/carbonyl")
	}

	args := []string{"--adblock", "--vim"}
	if c.graphics {
		args = append(args, "--graphics")
	} else {
		// no kitty protocol to render them, so skip the download entirely
		args = append(args, "--no-images")
	}
	cmd := exec.Command(bin, append(args, c.url)...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer ptmx.Close()

	tty, ok := c.stdin.(*os.File)
	if !ok {
		tty = os.Stdin
	}

	// bubbletea released the terminal into cooked mode; carbonyl expects raw
	if state, err := term.MakeRaw(tty.Fd()); err == nil {
		defer term.Restore(tty.Fd(), state)
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			pty.InheritSize(tty, ptmx)
		}
	}()
	pty.InheritSize(tty, ptmx)

	input, err := cancelreader.NewReader(tty)
	if err != nil {
		return err
	}

	var quit, browse atomic.Bool
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := input.Read(buf)
			if err != nil {
				return
			}
			// a lone q quits; a lone b quits and opens the page in the browser.
			// inside a longer chunk they're part of an escape sequence or paste,
			// so they belong to the page
			if n == 1 && (buf[0] == 'q' || buf[0] == 'b') {
				browse.Store(buf[0] == 'b')
				quit.Store(true)
				// pty.Start setsid'd carbonyl; -pid takes its renderers too
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return
			}
			if _, err := ptmx.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	outDone := make(chan struct{})
	go func() {
		io.Copy(c.stdout, ptmx)
		close(outDone)
	}()

	err = cmd.Wait()
	input.Cancel()
	ptmx.Close() // unblock the output copy if a straggler keeps the pty open
	<-outDone

	// a killed carbonyl never restores the modes it set on the real terminal
	// (mouse tracking, alt screen, cursor); undo them before bubbletea resumes
	io.WriteString(c.stdout, "\x1b[?1003l\x1b[?1006l\x1b[?1049l\x1b[?25h")

	c.openBrowser = browse.Load()
	if quit.Load() {
		return nil
	}
	return err
}
