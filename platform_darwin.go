package main

// The chimes macOS has always used. They ship with the system, so there is
// nothing to install alongside the binary.
var (
	startChime = "/System/Library/Sounds/Blow.aiff"
	endChime   = "/System/Library/Sounds/Bottle.aiff"
)

// holdKeyName is what the ready line calls the push-to-talk key. On a Mac it
// is always fn; keycode 63 exists on every Mac keyboard and does nothing on
// its own.
const holdKeyName = "fn"
