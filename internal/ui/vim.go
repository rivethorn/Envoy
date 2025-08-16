package ui

import (
	"fmt"
	"strconv"
	"strings"
)

type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeCommand
	ModeSearch
)

type VimState struct {
	Mode         Mode
	PendingNum   string
	PendingOp    string
	LastSearch   string
	StatusFn     func(s string)
	RedrawFn     func()
	MoveFn       func(dy, dx int)
	JumpTopFn    func()
	JumpBottomFn func()
	EditFn       func(append bool)
	AddFn        func()
	DeleteFn     func()
	NextMatchFn  func(prev bool)
	CommandFn    func(cmd string) string
	SearchFn     func(query string)
	CancelFn     func()
}

// NewVimState return a vim state as normal mode
func NewVimState() *VimState {
	return &VimState{Mode: ModeNormal}
}

// SetStatus does exactly what it says.
func (v *VimState) SetStatus(format string, a ...any) {
	if v.StatusFn != nil {
		v.StatusFn(fmt.Sprintf(format, a...))
	}
}

func (v *VimState) resetPrefix() {
	v.PendingNum = ""
	v.PendingOp = ""
}

func (v *VimState) countOrDefault() int {
	if v.PendingNum == "" {
		return 1
	}
	n, _ := strconv.Atoi(v.PendingNum)
	if n <= 0 {
		return 1
	}
	return n
}

// HandleKey consumes a key string (already normalized) in NORMAL mode.
// Retures true if handled.
func (v *VimState) HandleKey(key string) bool {
	switch v.Mode {
	case ModeNormal:
		return v.handleNormal(key)
	default:
		return false
	}
}
func (v *VimState) handleNormal(key string) bool {
	if key >= "0" && key <= "9" {
		if !(v.PendingNum == "" && key == "0") {
			v.PendingNum += key
			v.SetStatus("-- %s", v.prefixText())
			return true
		}
	}
	// sequence handling
	if v.PendingOp == "" {
		switch key {
		case "h":
			v.MoveFn(0, -1)
		case "l":
			v.MoveFn(0, 1)
		case "j":
			v.MoveFn(v.countOrDefault(), 0)
		case "k":
			v.MoveFn(-v.countOrDefault(), 0)
		case "g":
			v.PendingOp = "g"
			v.SetStatus("-- g")
			return true
		case "G":
			v.JumpBottomFn()
		case "0":
			// first column
			v.MoveFn(0, -9999)
		case "$":
			// last column
			v.MoveFn(0, 9999)
		case "/":
			v.Mode = ModeSearch
			v.SetStatus("/")
		case "n":
			v.NextMatchFn(false)
		case "N":
			v.NextMatchFn(true)
		case ":":
			v.Mode = ModeCommand
			v.SetStatus(":")
		case "i":
			v.Mode = ModeInsert
			v.EditFn(false)
		case "a":
			v.Mode = ModeInsert
			v.EditFn(true)
		case "A":
			v.AddFn()
		case "x":
			v.DeleteFn()
		case "ESC":
			v.CancelFn()
		default:
			// ignore
			v.resetPrefix()
			return false
		}
	} else {
		// we have a pending op
		switch v.PendingOp {
		case "g":
			if key == "g" {
				v.JumpTopFn()
			}
		}
	}
	v.resetPrefix()
	return true
}

func (v *VimState) prefixText() string {
	var b strings.Builder
	if v.PendingNum != "" {
		b.WriteString(v.PendingNum)
	}
	if v.PendingOp != "" {
		b.WriteString(v.PendingOp)
	}
	return b.String()
}
