package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rivethorn/envoy/internal/env"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	pageMain  = "main"
	pageModal = "modal"
)

type App struct {
	App    *tview.Application
	Pages  *tview.Pages
	Table  *tview.Table
	Status *tview.TextView
	Cmd    *tview.InputField
	Layout *tview.Flex

	Store *env.Store
	Vim   *VimState

	selRow     int // 1-based (0 is header)
	selCol     int // 0=KEY, 1=VALUE
	lastFilter string
}

func Run() error {
	a := NewApp()
	return a.App.Run()
}

func NewApp() *App {
	app := tview.NewApplication()

	store := env.NewStore()

	table := tview.NewTable().
		SetBorders(false).
		SetFixed(1, 0).
		SetSelectable(true, true) // enable row & column selection
	table.SetBorder(true).SetTitle(" Environment variables ")

	status := tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(false).
		SetTextAlign(tview.AlignLeft)
	status.SetBorder(false)

	cmd := tview.NewInputField().
		SetLabel("").
		SetFieldWidth(0)
	cmd.SetBorder(false)

	pages := tview.NewPages()
	main := tview.NewFlex().SetDirection(tview.FlexRow)
	main.AddItem(table, 0, 1, true)
	main.AddItem(cmd, 1, 0, false)
	main.AddItem(status, 1, 0, false)
	pages.AddPage(pageMain, main, true, true)

	a := &App{
		App:    app,
		Pages:  pages,
		Table:  table,
		Status: status,
		Cmd:    cmd,
		Layout: main,
		Store:  store,
		Vim:    NewVimState(),
	}

	a.initVim()
	a.hookHandlers()
	a.renderTable()
	a.setSelection(1, 0) // first data row, KEY column
	a.updateStatusHint("NORMAL")

	app.SetRoot(pages, true)
	return a
}

func (a *App) initVim() {
	a.Vim.StatusFn = func(s string) { a.updateStatusInline(s) }
	a.Vim.RedrawFn = func() { a.renderTable() }
	a.Vim.MoveFn = func(dy, dx int) { a.move(dy, dx) }
	a.Vim.JumpTopFn = func() { a.jumpTop() }
	a.Vim.JumpBottomFn = func() { a.jumpBottom() }
	a.Vim.EditFn = func(append bool) { a.openEditForm(append) }
	a.Vim.AddFn = func() { a.openAddForm() }
	a.Vim.DeleteFn = func() { a.confirmDelete() }
	a.Vim.NextMatchFn = func(prev bool) { a.nextMatch(prev) }
	a.Vim.CommandFn = func(cmd string) string { return a.execCommand(cmd) }
	a.Vim.SearchFn = func(q string) { a.applySearch(q) }
	a.Vim.CancelFn = func() { a.exitMini() }
}

func (a *App) hookHandlers() {
	// Table input capture: Normal-mode keys, plus ":" and "/" to open minibuffer.
	a.Table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		key := normalizeKey(ev)
		switch a.Vim.Mode {
		case ModeNormal:
			if key == ":" {
				a.enterCommand("")
				return nil
			}
			if key == "/" {
				a.enterSearch("")
				return nil
			}
			if key == "q" {
				a.updateStatusInline("Use :q to quit")
				return nil
			}
			if a.Vim.HandleKey(key) {
				return nil
			}
		case ModeInsert:
			// Forms handle input while in INSERT mode.
			return ev
		case ModeCommand, ModeSearch:
			// Focus will be on Cmd; ignore here.
			return ev
		}
		return ev
	})

	// Track selection changes regardless of input source (mouse, arrows, hjkl).
	a.Table.SetSelectionChangedFunc(func(row, column int) {
		a.selRow = row
		a.selCol = column
	})

	// Command/search minibuffer: Enter applies, ESC cancels, others ignored.
	a.Cmd.SetDoneFunc(func(key tcell.Key) {
		text := a.Cmd.GetText()
		switch a.Vim.Mode {
		case ModeCommand:
			switch key {
			case tcell.KeyEnter:
				out := a.execCommand(strings.TrimSpace(text))
				if out != "" {
					a.updateStatusInline(out)
				}
				a.exitMini()
			case tcell.KeyEsc:
				a.exitMini()
			default:
				// ignore Tab/Backtab etc.
			}
		case ModeSearch:
			switch key {
			case tcell.KeyEnter:
				a.applySearch(text)
				a.exitMini()
			case tcell.KeyEsc:
				a.exitMini()
			default:
				// ignore
			}
		default:
			a.exitMini()
		}
	})

	// Incremental search.
	a.Cmd.SetChangedFunc(func(text string) {
		if a.Vim.Mode == ModeSearch {
			a.applySearch(text)
		}
	})
}

func normalizeKey(ev *tcell.EventKey) string {
	switch ev.Key() {
	case tcell.KeyEsc:
		return "ESC"
	case tcell.KeyEnter:
		return "ENTER"
	case tcell.KeyUp:
		return "k"
	case tcell.KeyDown:
		return "j"
	case tcell.KeyLeft:
		return "h"
	case tcell.KeyRight:
		return "l"
	default:
		if r := ev.Rune(); r != 0 {
			return string(r)
		}
		return ""
	}
}

func (a *App) renderTable() {
	a.Table.Clear()

	// Header
	a.Table.SetCell(0, 0, headerCell("KEY"))
	a.Table.SetCell(0, 1, headerCell("VALUE"))

	keys := a.Store.ListKeys()
	for i, k := range keys {
		row := i + 1
		item, _ := a.Store.GetByIndex(i)

		keyCell := tview.NewTableCell(k).
			SetExpansion(1).
			SetSelectable(true)
		valCell := tview.NewTableCell(item.Value).
			SetExpansion(3).
			SetSelectable(true)

		if item.Modified {
			keyCell.SetTextColor(tcell.ColorYellow)
			valCell.SetTextColor(tcell.ColorYellow)
		}

		a.Table.SetCell(row, 0, keyCell)
		a.Table.SetCell(row, 1, valCell)
	}

	// Reselect within bounds.
	max := a.Store.Count()
	if max == 0 {
		a.selRow = 0
		a.selCol = 0
		a.Table.Select(0, 0)
	} else {
		if a.selRow < 1 {
			a.selRow = 1
		}
		if a.selRow > max {
			a.selRow = max
		}
		if a.selCol < 0 {
			a.selCol = 0
		}
		if a.selCol > 1 {
			a.selCol = 1
		}
		a.Table.Select(a.selRow, a.selCol)
	}

	a.refreshStatus()
}

func headerCell(s string) *tview.TableCell {
	return tview.NewTableCell("[::b]" + s).
		SetTextColor(tcell.ColorWhite).
		SetAlign(tview.AlignLeft).
		SetSelectable(false).
		SetBackgroundColor(tcell.ColorDarkBlue)
}

func (a *App) updateStatusInline(s string) {
	a.Status.SetText(" " + s)
}

func (a *App) refreshStatus() {
	mode := "NORMAL"
	switch a.Vim.Mode {
	case ModeInsert:
		mode = "INSERT"
	case ModeCommand:
		mode = "COMMAND"
	case ModeSearch:
		mode = "SEARCH"
	}
	count := a.Store.Count()
	hints := "[A]dd [i/a] Edit [x] Delete [/ ] Search [:] Cmd (n/N to cycle) | :w :q :import"
	a.Status.SetText(fmt.Sprintf(" %s | %d vars | %s", mode, count, hints))
}

func (a *App) updateStatusHint(mode string) {
	count := a.Store.Count()
	hints := "[A]dd [i/a] Edit [x] Delete [/ ] Search [:] Cmd (n/N to cycle) | :w :q :import"
	a.Status.SetText(fmt.Sprintf(" %s | %d vars | %s", mode, count, hints))
}

func (a *App) move(dy, dx int) {
	// Rows: 0 header; data start at 1.
	newRow := a.selRow + dy
	maxRow := a.Store.Count()
	if maxRow < 1 {
		newRow = 0
	} else {
		if newRow < 1 {
			newRow = 1
		}
		if newRow > maxRow {
			newRow = maxRow
		}
	}

	// Columns: 0..1
	newCol := a.selCol + dx
	if dx <= -9999 {
		newCol = 0
	} else if dx >= 9999 {
		newCol = 1
	} else {
		if newCol < 0 {
			newCol = 0
		}
		if newCol > 1 {
			newCol = 1
		}
	}

	a.setSelection(newRow, newCol)
}

func (a *App) setSelection(row, col int) {
	a.selRow = row
	a.selCol = col
	a.Table.Select(a.selRow, a.selCol)
}

func (a *App) jumpTop() {
	if a.Store.Count() > 0 {
		a.setSelection(1, a.selCol)
	}
}

func (a *App) jumpBottom() {
	n := a.Store.Count()
	if n > 0 {
		a.setSelection(n, a.selCol)
	}
}

func (a *App) openEditForm(append bool) {
	idx := a.selRow - 1
	item, ok := a.Store.GetByIndex(idx)
	if !ok {
		return
	}

	form := tview.NewForm().
		AddInputField("Key", item.Key, 40, nil, nil).
		AddInputField("Value", item.Value, 60, nil, nil)

	saveBtn := func() {
		key := form.GetFormItemByLabel("Key").(*tview.InputField).GetText()
		val := form.GetFormItemByLabel("Value").(*tview.InputField).GetText()
		key = strings.TrimSpace(key)
		if key == "" {
			a.updateStatusInline("Key cannot be empty")
			return
		}
		a.Store.Upsert(key, val)
		a.closeModal()
		// Re-select edited key.
		a.selectKey(key)
		a.updateStatusInline(fmt.Sprintf("Saved %s", key))
		a.Vim.Mode = ModeNormal
		a.refreshStatus()
	}

	form.AddButton("Save", saveBtn).
		AddButton("Cancel", func() {
			a.closeModal()
			a.Vim.Mode = ModeNormal
			a.refreshStatus()
		})
	form.SetBorder(true).SetTitle(" Edit variable ").SetTitleAlign(tview.AlignLeft)

	if append {
		// no explicit caret API; keep value as-is to simulate append behavior
		if iv, ok := form.GetFormItemByLabel("Value").(*tview.InputField); ok {
			val := iv.GetText()
			iv.SetText(val)
		}
	}

	a.Vim.Mode = ModeInsert
	modal := centerPrimitive(form, 80, 10)
	a.Pages.AddPage(pageModal, modal, true, true)
	a.App.SetFocus(form)
	a.refreshStatus()
}

func (a *App) openAddForm() {
	form := tview.NewForm().
		AddInputField("Key", "", 40, nil, nil).
		AddInputField("Value", "", 60, nil, nil)

	addBtn := func() {
		key := strings.TrimSpace(form.GetFormItemByLabel("Key").(*tview.InputField).GetText())
		val := form.GetFormItemByLabel("Value").(*tview.InputField).GetText()
		if key == "" {
			a.updateStatusInline("Key cannot be empty")
			return
		}
		a.Store.Upsert(key, val)
		a.closeModal()
		a.renderTable()
		a.selectKey(key)
		a.updateStatusInline(fmt.Sprintf("Added %s", key))
		a.Vim.Mode = ModeNormal
		a.refreshStatus()
	}

	form.AddButton("Add", addBtn).
		AddButton("Cancel", func() {
			a.closeModal()
			a.Vim.Mode = ModeNormal
			a.refreshStatus()
		})
	form.SetBorder(true).SetTitle(" Add variable ").SetTitleAlign(tview.AlignLeft)

	a.Vim.Mode = ModeInsert
	modal := centerPrimitive(form, 80, 10)
	a.Pages.AddPage(pageModal, modal, true, true)
	a.App.SetFocus(form)
	a.refreshStatus()
}

func (a *App) confirmDelete() {
	idx := a.selRow - 1
	item, ok := a.Store.GetByIndex(idx)
	if !ok {
		return
	}

	m := tview.NewModal().
		SetText(fmt.Sprintf("Delete %s?", item.Key)).
		AddButtons([]string{"Yes", "No"}).
		SetDoneFunc(func(_ int, label string) {
			if label == "Yes" {
				a.Store.Delete(item.Key)
				a.renderTable()
				if a.selRow > a.Store.Count() {
					a.selRow = a.Store.Count()
					if a.selRow < 1 {
						a.selRow = 1
					}
					a.Table.Select(a.selRow, a.selCol)
				}
				a.updateStatusInline(fmt.Sprintf("Deleted %s", item.Key))
			}
			a.closeModal()
			a.Vim.Mode = ModeNormal
			a.refreshStatus()
		})
	a.Pages.AddPage(pageModal, centerPrimitive(m, 50, 7), true, true)
	a.App.SetFocus(m)
}

func (a *App) selectKey(key string) {
	keys := a.Store.ListKeys()
	for i, k := range keys {
		if k == key {
			a.setSelection(i+1, a.selCol)
			return
		}
	}
}

func centerPrimitive(p tview.Primitive, width, height int) tview.Primitive {
	// Outer vertical: top spacer, middle row (with content centered), bottom spacer.
	outer := tview.NewFlex().SetDirection(tview.FlexRow)
	outer.AddItem(nil, 0, 1, false)

	mid := tview.NewFlex().SetDirection(tview.FlexColumn)
	mid.AddItem(nil, 0, 1, false)
	mid.AddItem(p, width, 0, true) // fixed width
	mid.AddItem(nil, 0, 1, false)

	outer.AddItem(mid, height, 0, true) // fixed height
	outer.AddItem(nil, 0, 1, false)
	return outer
}

func (a *App) enterCommand(prefill string) {
	a.Vim.Mode = ModeCommand
	a.Cmd.SetLabel(":")
	a.Cmd.SetText(prefill)
	a.App.SetFocus(a.Cmd)
	a.refreshStatus()
}

func (a *App) enterSearch(prefill string) {
	a.Vim.Mode = ModeSearch
	a.Cmd.SetLabel("/")
	a.Cmd.SetText(prefill)
	a.App.SetFocus(a.Cmd)
	a.refreshStatus()
}

func (a *App) exitMini() {
	a.Cmd.SetText("")
	a.Cmd.SetLabel("")
	a.App.SetFocus(a.Table)
	a.Vim.Mode = ModeNormal
	a.refreshStatus()
}

func (a *App) applySearch(q string) {
	a.Store.Filter(q)
	a.lastFilter = q
	a.renderTable()
	if a.Store.Count() >= 1 {
		a.setSelection(1, a.selCol)
	}
	if q == "" {
		a.updateStatusInline("Filter cleared")
	} else {
		a.updateStatusInline("Filter: " + q)
	}
}

func (a *App) nextMatch(prev bool) {
	if a.lastFilter == "" {
		return
	}
	n := a.Store.Count()
	if n <= 1 {
		return
	}
	if prev {
		if a.selRow <= 1 {
			a.selRow = n
		} else {
			a.selRow--
		}
	} else {
		if a.selRow >= n {
			a.selRow = 1
		} else {
			a.selRow++
		}
	}
	a.Table.Select(a.selRow, a.selCol)
}

func (a *App) execCommand(text string) string {
	// Strip leading ":" if present.
	text = strings.TrimPrefix(strings.TrimSpace(text), ":")
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	cmd := fields[0]
	args := fields[1:]

	switch cmd {
	case "q", "quit":
		a.App.Stop()
	case "w":
		path := ".env"
		if len(args) >= 1 {
			path = strings.Join(args, " ")
		}
		if !filepath.IsAbs(path) && strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		if err := a.Store.Export(path); err != nil {
			return fmt.Sprintf("Write failed: %v", err)
		}
		return fmt.Sprintf("Wrote %s", path)
	case "wq":
		msg := a.execCommand("w " + strings.Join(args, " "))
		a.App.Stop()
		return msg
	case "x":
		if a.Store.Dirty() {
			_ = a.execCommand("w " + strings.Join(args, " "))
		}
		a.App.Stop()
	case "import":
		if len(args) < 1 {
			return "Usage: :import <path>"
		}
		path := strings.Join(args, " ")
		if !filepath.IsAbs(path) && strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		n, err := a.Store.Import(path)
		if err != nil {
			return fmt.Sprintf("Import failed: %v", err)
		}
		a.renderTable()
		return fmt.Sprintf("Imported %d vars from %s", n, path)
	case "e", "edit":
		a.Store.LoadFromProcess()
		a.renderTable()
		return "Reloaded from process environment"
	case "help", "h", "?":
		return "Commands: :w [path] | :q | :wq | :x | :import <path> | :e | /search"
	default:
		return fmt.Sprintf("Unknown command: %s", cmd)
	}
	return ""
}

func (a *App) closeModal() {
	a.Pages.RemovePage(pageModal)
	a.App.SetFocus(a.Table)
}
