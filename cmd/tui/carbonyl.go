//go:build !windows

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
)

// openCarbonyl renders the URL in carbonyl full screen, suspending the launcher
// until it exits. Ported from the individual apps so the "all" view reads a
// story the same way.
func openCarbonyl(url string, graphics bool) tea.Cmd {
	c := &carbonylCmd{url: url, graphics: graphics}
	return tea.Exec(c, func(err error) tea.Msg {
		if err != nil {
			return errMsg{err}
		}
		if c.openBrowser {
			return carbonylBrowseMsg{url: c.url}
		}
		return carbonylDoneMsg{}
	})
}

// carbonylCmd runs carbonyl on a pty so a bare q quits back to the list and a
// bare b quits then opens the URL in the browser; carbonyl itself only exits on
// ctrl+c and would otherwise send those keys to the page.
type carbonylCmd struct {
	url         string
	graphics    bool
	openBrowser bool
	stdin       io.Reader
	stdout      io.Writer
}

func (c *carbonylCmd) SetStdin(r io.Reader)  { c.stdin = r }
func (c *carbonylCmd) SetStdout(w io.Writer) { c.stdout = w }
func (c *carbonylCmd) SetStderr(io.Writer)   {}

func (c *carbonylCmd) Run() error {
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
