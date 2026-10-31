package tui

import "github.com/gdamore/tcell/v2"

// lfPasteScreen wraps the terminal screen so a bare LF inside a
// bracketed paste reaches tview's paste assembly as a newline. tcell
// reports LF (0x0a) as Ctrl-J, which tview silently drops while
// collecting a paste, so "one\ntwo" pasted from a terminal that sends
// LF would arrive as "onetwo". CR (what most terminals send) is already
// KeyEnter and passes through unchanged, and the LF of a CRLF pair is
// still dropped so CRLF stays a single newline. Outside a paste nothing
// is translated, so Ctrl-J keeps its normal meaning.
type lfPasteScreen struct {
	tcell.Screen
	pasting bool // PollEvent has a single caller (tview's event reader)
	afterCR bool // previous pasted key was CR (Enter)
	initErr error
}

func (s *lfPasteScreen) Init() error {
	s.initErr = s.Screen.Init()
	return s.initErr
}

func (s *lfPasteScreen) PollEvent() tcell.Event {
	ev := s.Screen.PollEvent()
	switch e := ev.(type) {
	case *tcell.EventPaste:
		s.pasting, s.afterCR = e.Start(), false
	case *tcell.EventKey:
		if !s.pasting {
			break
		}
		lf, cr := e.Key() == tcell.KeyCtrlJ, e.Key() == tcell.KeyEnter
		prevCR := s.afterCR
		s.afterCR = cr
		if lf && !prevCR {
			return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
		}
	}
	return ev
}

// installScreen creates the terminal screen wrapped in lfPasteScreen
// and hands it to tview (which initialises it). Call before runUI.
func (a *App) installScreen() error {
	s, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	w := &lfPasteScreen{Screen: s}
	a.tv.SetScreen(w)
	return w.initErr
}
