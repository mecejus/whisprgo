// The hold-key combination: how "ctrl+win" in config.json becomes something
// the keyboard hook can test on every keystroke.
//
// Deliberately not behind a build tag even though only Windows reads it. This
// is the most intricate logic in the Windows half — order independence,
// either-side modifiers, auto-repeat — and untagged it can be tested on any
// machine rather than only on a Windows CI runner. The macOS binary never
// references it, so the linker drops it.

package keyboard

import (
	"fmt"
	"strings"
)

// Virtual-key codes. A low-level hook reports the side-distinguished codes
// (VK_LCONTROL rather than VK_CONTROL), which is what makes "either ctrl"
// something we have to spell out rather than get for free.
const (
	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLShift   = 0xA0
	vkRShift   = 0xA1
	vkLMenu    = 0xA4
	vkRMenu    = 0xA5
	vkLWin     = 0x5B
	vkRWin     = 0x5C
	vkApps     = 0x5D
	vkCapital  = 0x14
	vkPause    = 0x13
	vkScroll   = 0x91
	vkF13      = 0x7C

	// vkNoName is documented by Microsoft only as reserved. Nothing maps it,
	// which is exactly what makes it useful: see maskWin.
	vkNoName = 0xFC
)

// maxParts caps how many keys a hold combination may name. Three is already
// more than anyone should have to hold down to start talking.
const maxParts = 3

// keyNames maps what may appear in config.json to the virtual-key codes that
// satisfy it. A bare modifier name accepts either side of the keyboard; the
// prefixed forms pin it to one.
var keyNames = map[string][]uint32{
	"ctrl":    {vkLControl, vkRControl},
	"control": {vkLControl, vkRControl},
	"shift":   {vkLShift, vkRShift},
	"alt":     {vkLMenu, vkRMenu},
	"win":     {vkLWin, vkRWin},
	"windows": {vkLWin, vkRWin},
	"super":   {vkLWin, vkRWin},
	"cmd":     {vkLWin, vkRWin},

	"leftctrl":   {vkLControl},
	"lctrl":      {vkLControl},
	"rightctrl":  {vkRControl},
	"rctrl":      {vkRControl},
	"leftshift":  {vkLShift},
	"lshift":     {vkLShift},
	"rightshift": {vkRShift},
	"rshift":     {vkRShift},
	"leftalt":    {vkLMenu},
	"lalt":       {vkLMenu},
	"rightalt":   {vkRMenu},
	"ralt":       {vkRMenu},
	"leftwin":    {vkLWin},
	"lwin":       {vkLWin},
	"rightwin":   {vkRWin},
	"rwin":       {vkRWin},

	"menu":       {vkApps},
	"apps":       {vkApps},
	"capslock":   {vkCapital},
	"pause":      {vkPause},
	"scrolllock": {vkScroll},
}

// canonical spells a key group back out for the ready line, so what the user
// is told to hold matches what they configured.
var canonical = map[uint32]string{
	vkLControl: "left ctrl", vkRControl: "right ctrl",
	vkLShift: "left shift", vkRShift: "right shift",
	vkLMenu: "left alt", vkRMenu: "right alt",
	vkLWin: "left win", vkRWin: "right win",
	vkApps: "menu", vkCapital: "caps lock",
	vkPause: "pause", vkScroll: "scroll lock",
}

func init() {
	for i := 0; i < 12; i++ {
		vk := uint32(vkF13 + i)
		name := fmt.Sprintf("f%d", 13+i)
		keyNames[name] = []uint32{vk}
		canonical[vk] = strings.ToUpper(name)
	}
}

// DefaultHoldKey is what whisprgo listens for unless config.json says
// otherwise. Windows has no fn key to borrow — on nearly all laptops it is
// handled inside the keyboard and never reaches the OS — and no single key is
// as free of a second meaning as fn is on a Mac. Two modifiers together are:
// nothing in Windows is bound to ctrl+win on its own.
const DefaultHoldKey = "ctrl+win"

// holdSpec is a parsed hold key: one or more parts, all of which must be held
// at once. It is built by Configure and only ever read afterwards, so the
// hook thread can use it without a lock.
type holdSpec struct {
	parts [maxParts][]uint32
	n     int

	// display is what the ready line calls it.
	display string

	// swallow hides the key from the rest of the system. Only a single-part
	// hold key can do this: eating half of a combination would leave the
	// other half's modifier state inconsistent with what applications see.
	swallow bool

	// masksWin says a Windows key is part of this combination. Releasing the
	// Windows key opens the Start menu unless some other key was pressed
	// while it was down, so one has to be — see maskWin.
	masksWin bool
}

// parseHoldSpec turns "ctrl+win" into something the hook can test cheaply.
func parseHoldSpec(name string, passThrough bool) (*holdSpec, error) {
	fields := strings.Split(strings.ToLower(strings.TrimSpace(name)), "+")

	spec := &holdSpec{}
	var labels []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if spec.n == maxParts {
			return nil, fmt.Errorf("hold key %q names more than %d keys", name, maxParts)
		}
		vks, ok := keyNames[f]
		if !ok {
			return nil, fmt.Errorf("unknown hold key %q", f)
		}
		for _, vk := range vks {
			if vk == vkLWin || vk == vkRWin {
				spec.masksWin = true
			}
		}
		spec.parts[spec.n] = vks
		spec.n++
		labels = append(labels, label(f, vks))
	}

	if spec.n == 0 {
		return nil, fmt.Errorf("hold key is empty")
	}

	spec.display = strings.Join(labels, " + ")
	// A combination is never swallowed. Its parts are real modifiers that
	// applications and the OS track independently, and hiding a key-down
	// whose key-up they do see (or the reverse) is how a modifier gets stuck
	// on. A single dedicated key has no such problem.
	spec.swallow = spec.n == 1 && !passThrough
	return spec, nil
}

// label names a part. A bare modifier covers both sides, so it keeps the
// short name rather than picking one arbitrarily.
func label(name string, vks []uint32) string {
	if len(vks) > 1 {
		return name
	}
	if c, ok := canonical[vks[0]]; ok {
		return c
	}
	return name
}

// matches reports which part of the combination a virtual-key code belongs
// to, and which of that part's codes it is. Called for every keystroke in the
// session, so it stays a pair of tiny loops over at most three parts of at
// most two codes.
func (s *holdSpec) matches(vk uint32) (part, slot int, ok bool) {
	for p := 0; p < s.n; p++ {
		for i, candidate := range s.parts[p] {
			if candidate == vk {
				return p, i, true
			}
		}
	}
	return 0, 0, false
}

// keyChange is one hold-key transition on its way from the hook thread to the
// goroutine that owns the state machine.
type keyChange struct {
	vk   uint32
	down bool
}

// tracker is the hold-key state machine. It lives on one goroutine — the same
// one that runs the callbacks — so none of it needs synchronising.
type tracker struct {
	// held[p] has a bit set for each of part p's virtual-key codes currently
	// down. A part is satisfied while its bits are not all clear, which is
	// what lets "ctrl" mean either ctrl without caring which.
	held [maxParts]uint8

	active bool
}

func (t *tracker) apply(s *holdSpec, c keyChange) (changed bool) {
	part, slot, ok := s.matches(c.vk)
	if !ok {
		return false
	}
	before := t.active
	if c.down {
		t.held[part] |= 1 << slot
	} else {
		t.held[part] &^= 1 << slot
	}
	t.active = t.satisfied(s)
	return t.active != before
}

func (t *tracker) satisfied(s *holdSpec) bool {
	for p := 0; p < s.n; p++ {
		if t.held[p] == 0 {
			return false
		}
	}
	return true
}

func (t *tracker) reset() {
	t.held = [maxParts]uint8{}
	t.active = false
}
