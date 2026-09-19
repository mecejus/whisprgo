package keyboard

import "testing"

func mustParse(t *testing.T, name string, passThrough bool) *holdSpec {
	t.Helper()
	s, err := parseHoldSpec(name, passThrough)
	if err != nil {
		t.Fatalf("parseHoldSpec(%q): %v", name, err)
	}
	return s
}

func TestDefaultHoldKeyIsCtrlWin(t *testing.T) {
	s := mustParse(t, DefaultHoldKey, false)
	if s.n != 2 {
		t.Fatalf("got %d parts, want 2", s.n)
	}
	if s.display != "ctrl + win" {
		t.Errorf("display is %q, want %q", s.display, "ctrl + win")
	}
	if !s.masksWin {
		t.Error("a combination containing win must mask the Start menu")
	}
	// Swallowing half a combination is how a modifier gets stuck on.
	if s.swallow {
		t.Error("a combination must not be swallowed")
	}
}

func TestSingleKeyIsSwallowedUnlessPassedThrough(t *testing.T) {
	if s := mustParse(t, "capslock", false); !s.swallow {
		t.Error("a single hold key should be swallowed by default")
	}
	if s := mustParse(t, "capslock", true); s.swallow {
		t.Error("pass_through_hold_key should stop the key being swallowed")
	}
	if s := mustParse(t, "capslock", false); s.masksWin {
		t.Error("caps lock has nothing to do with the Start menu")
	}
}

func TestParseRejectsNonsense(t *testing.T) {
	for _, name := range []string{"", "   ", "banana", "ctrl+banana", "ctrl+alt+shift+win", "+"} {
		if s, err := parseHoldSpec(name, false); err == nil {
			t.Errorf("parseHoldSpec(%q) returned %+v, want an error", name, s)
		}
	}
}

func TestParseNamesEachPart(t *testing.T) {
	for name, want := range map[string]string{
		"ctrl+win":   "ctrl + win",
		"rightctrl":  "right ctrl",
		"f13":        "F13",
		"CTRL + WIN": "ctrl + win",
		"alt+shift":  "alt + shift",
	} {
		if got := mustParse(t, name, false).display; got != want {
			t.Errorf("%q displays as %q, want %q", name, got, want)
		}
	}
}

// feed runs a sequence of transitions through the tracker and records when it
// went active and inactive, which is exactly what drives start and stop.
func feed(s *holdSpec, changes ...keyChange) []bool {
	var t tracker
	var fired []bool
	for _, c := range changes {
		if t.apply(s, c) {
			fired = append(fired, t.active)
		}
	}
	return fired
}

func down(vk uint32) keyChange { return keyChange{vk: vk, down: true} }
func up(vk uint32) keyChange   { return keyChange{vk: vk, down: false} }

func wantFired(t *testing.T, label string, got, want []bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: fired %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: fired %v, want %v", label, got, want)
		}
	}
}

// Whichever key the user reaches for first, the combination completes on the
// second one and ends on the first release.
func TestChordIsOrderIndependent(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)

	wantFired(t, "ctrl first",
		feed(s, down(vkLControl), down(vkLWin), up(vkLWin), up(vkLControl)),
		[]bool{true, false})

	wantFired(t, "win first",
		feed(s, down(vkLWin), down(vkLControl), up(vkLControl), up(vkLWin)),
		[]bool{true, false})
}

// Half a combination is not the combination.
func TestChordNeedsEveryPart(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)
	wantFired(t, "ctrl alone", feed(s, down(vkLControl), up(vkLControl)), nil)
	wantFired(t, "win alone", feed(s, down(vkRWin), up(vkRWin)), nil)
}

// "ctrl" means either ctrl. The user should not have to know which one the
// config meant.
func TestEitherSideSatisfiesAModifier(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)
	wantFired(t, "right ctrl, right win",
		feed(s, down(vkRControl), down(vkRWin), up(vkRWin)),
		[]bool{true, false})
	wantFired(t, "left ctrl, right win",
		feed(s, down(vkLControl), down(vkRWin), up(vkRWin)),
		[]bool{true, false})
}

// A named side must not answer to the other one.
func TestPinnedSideIgnoresTheOther(t *testing.T) {
	s := mustParse(t, "rightctrl", false)
	wantFired(t, "left ctrl", feed(s, down(vkLControl), up(vkLControl)), nil)
	wantFired(t, "right ctrl", feed(s, down(vkRControl), up(vkRControl)), []bool{true, false})
}

// Windows repeats key-down while a key is held. Only the transitions are
// dictation boundaries; a repeat must not restart the recording.
func TestAutoRepeatDoesNotRefire(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)
	wantFired(t, "repeats",
		feed(s,
			down(vkLControl), down(vkLControl),
			down(vkLWin), down(vkLWin), down(vkLControl),
			up(vkLWin)),
		[]bool{true, false})
}

// Someone resting a hand on both ctrls should not end the dictation by
// lifting one of them.
func TestReleasingOneOfTwoEquivalentKeysHolds(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)
	wantFired(t, "both ctrls",
		feed(s,
			down(vkLControl), down(vkRControl), down(vkLWin),
			up(vkLControl), // the other ctrl is still down
			up(vkRControl), // now the part is unsatisfied
		),
		[]bool{true, false})
}

// Keys outside the combination must not disturb it — the user carries on
// typing while a transcript is in flight.
func TestUnrelatedKeysAreIgnored(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)
	wantFired(t, "stray keys",
		feed(s, down(vkCapital), down(vkLControl), down(vkApps), down(vkLWin), up(vkCapital), up(vkLWin)),
		[]bool{true, false})
}

// After a forced release the state machine has to be usable again, or one
// locked workstation would cost the hotkey for the rest of the session. The
// key-ups never arrive in that case, so the next thing the tracker sees is a
// fresh key-down for a key it already believed was held.
func TestResetAllowsANewChord(t *testing.T) {
	s := mustParse(t, "ctrl+win", false)

	var tr tracker
	tr.apply(s, down(vkLControl))
	tr.apply(s, down(vkLWin))
	if !tr.active {
		t.Fatal("combination should be active")
	}

	tr.reset() // what the watchdog does when the keys are not really held
	if tr.active {
		t.Fatal("reset should clear the active flag")
	}

	var fired []bool
	for _, c := range []keyChange{down(vkLControl), down(vkLWin), up(vkLWin)} {
		if tr.apply(s, c) {
			fired = append(fired, tr.active)
		}
	}
	wantFired(t, "after reset", fired, []bool{true, false})
}
