package tui

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/major1201/flametui/pkg/profile"
	"github.com/major1201/flametui/pkg/render"
	"github.com/major1201/flametui/pkg/term"
)

// App is the main TUI application.
type App struct {
	profile  *profile.Profile
	renderer *render.FlameGraphRenderer

	width  int
	height int

	viewFrameID    int
	focusedStackID int
	sampleIndex    int

	scrollOffset      int
	stackScrollOffset int
	showInfoScreen    bool
	quitting          bool

	oldState *term.State
	fd       int
}

// NewApp creates a new TUI app.
func NewApp(prof *profile.Profile) *App {
	rend := render.NewFlameGraphRenderer(prof)

	sampleIdx := 0
	if prof.DefaultSampleTypeIndex >= 0 && prof.DefaultSampleTypeIndex < len(prof.SampleTypes) {
		sampleIdx = prof.DefaultSampleTypeIndex
	} else if prof.DefaultSampleTypeIndex < 0 {
		sampleIdx = len(prof.SampleTypes) + prof.DefaultSampleTypeIndex
	}

	rend.SampleIndex = sampleIdx
	rend.ViewFrameID = prof.RootStack.ID
	rend.FocusedStackID = prof.RootStack.ID

	return &App{
		profile:        prof,
		renderer:       rend,
		viewFrameID:    prof.RootStack.ID,
		focusedStackID: prof.RootStack.ID,
		sampleIndex:    sampleIdx,
	}
}

// Run starts the TUI application.
func (a *App) Run() error {
	a.fd = int(os.Stdin.Fd())

	// Save terminal state and set raw mode
	oldState, err := term.MakeRaw(a.fd)
	if err != nil {
		return fmt.Errorf("failed to set raw terminal: %w", err)
	}
	defer term.Restore(a.fd, oldState)
	a.oldState = oldState

	// Get terminal size
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}
	a.width = w
	a.height = h

	// Handle resize
	a.handleResize()

	// Switch to alternate screen
	fmt.Print("\033[?1049h")
	defer fmt.Print("\033[?1049l")

	// Enable mouse tracking
	fmt.Print("\033[?1000h\033[?1002h\033[?1006h")
	defer fmt.Print("\033[?1000l\033[?1002l\033[?1006l")

	// Hide cursor
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	a.render()

	// Read input
	buf := make([]byte, 32)
	for !a.quitting {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}
		a.handleInput(buf[:n])
	}

	return nil
}

func (a *App) handleInput(data []byte) {
	defer func() {
		if rec := recover(); rec != nil {
			a.quitting = true
			fmt.Print("\033[2J\033[H\033[?25h")
			fmt.Printf("PANIC: %v\r\n", rec)
		}
	}()

	if len(data) == 0 {
		return
	}

	// Mouse events
	if len(data) >= 3 && data[0] == 0x1b && data[1] == '[' && data[2] == '<' {
		a.handleMouse(data)
		return
	}

	// Escape key (single byte) vs escape sequences (multi-byte)
	if data[0] == 0x1b {
		if len(data) == 1 {
			// Esc key pressed alone
			if a.showInfoScreen {
				a.showInfoScreen = false
			} else {
				a.zoomOut()
			}
			a.render()
			return
		}
		if len(data) >= 3 {
			switch {
			case data[1] == '[' && data[2] == 'A': // up
				a.moveUp()
			case data[1] == '[' && data[2] == 'B': // down
				a.moveDown()
			case data[1] == '[' && data[2] == 'C': // right
				a.moveRight()
			case data[1] == '[' && data[2] == 'D': // left
				a.moveLeft()
			case data[1] == '[' && data[2] == 'H': // home
				a.scrollOffset = 0
			case data[1] == '[' && data[2] == 'F': // end
				a.scrollOffset = len(a.profile.Lines) - a.fgHeight()
			case data[1] == '[' && data[2] >= '1' && data[2] <= '6':
				// Extended mouse event - handled in handleMouse
				return
			}
		}
		a.render()
		return
	}

	// Single key
	switch data[0] {
	case 'q', 3: // ctrl+c
		a.quitting = true
	case 'h':
		a.moveLeft()
	case 'j':
		a.moveDown()
	case 'k':
		a.moveUp()
	case 'l':
		a.moveRight()
	case 13: // enter
		a.zoomIn()
	case 27: // esc
		if a.showInfoScreen {
			a.showInfoScreen = false
		} else {
			a.zoomOut()
		}
	case 'i':
		a.showInfoScreen = !a.showInfoScreen
	case '\t':
		a.sampleIndex = (a.sampleIndex + 1) % len(a.profile.SampleTypes)
		a.renderer.SampleIndex = a.sampleIndex
		a.stackScrollOffset = 0
	}

	a.render()
}

func (a *App) handleMouse(data []byte) {
	// Parse SGR mouse: \033[<Cb;Cx;CyM or \033[<Cb;Cx;Cym
	// Cb: button (0=left, 1=middle, 2=right, 32=move, 64=scroll up, 65=scroll down)
	// For press: M at end; for release: m at end
	parts := strings.Split(string(data[:len(data)-1]), ";")
	if len(parts) < 3 {
		return
	}

	// First part is "\033[<Cb"
	cbStr := strings.TrimPrefix(parts[0], "\033[<")
	var cb, cx, cy int
	fmt.Sscanf(cbStr, "%d", &cb)
	fmt.Sscanf(parts[1], "%d", &cx)
	fmt.Sscanf(parts[2], "%d", &cy)

	// Convert to 0-based
	cx--
	cy--

	switch {
	case cb == 0: // left click
		a.handleClick(cx, cy)
	case cb == 32: // motion
		a.handleMouseMove(cx, cy)
	case cb == 64: // scroll up
		a.handleScroll(cx, cy, -3)
	case cb == 65: // scroll down
		a.handleScroll(cx, cy, 3)
	}

	a.render()
}

func (a *App) handleClick(x, y int) {
	// Adjust for header
	fgY := y - a.headerHeight()
	if fgY < 0 {
		return
	}
	frame := a.renderer.GetFrameUnderMouse(x, fgY, a.width)
	if frame != nil {
		a.focusedStackID = frame.ID
		a.renderer.FocusedStackID = a.focusedStackID
		a.viewFrameID = frame.ID
		a.renderer.ViewFrameID = a.viewFrameID
		a.scrollOffset = 0
		a.stackScrollOffset = 0
	}
}

func (a *App) handleMouseMove(x, y int) {
	fgY := y - a.headerHeight()
	if fgY < 0 {
		return
	}
	frame := a.renderer.GetFrameUnderMouse(x, fgY, a.width)
	if frame != nil {
		a.viewFrameID = frame.ID
		a.renderer.ViewFrameID = a.viewFrameID
	}
}

func (a *App) handleScroll(x, y, delta int) {
	// Check if mouse is in the footer area
	footerStart := a.headerHeight() + a.fgHeight()
	if y >= footerStart {
		// Scroll the stack trace
		a.stackScrollOffset += delta
		if a.stackScrollOffset < 0 {
			a.stackScrollOffset = 0
		}
		return
	}
	// Otherwise scroll the flamegraph
	a.scrollOffset += delta
	if a.scrollOffset < 0 {
		a.scrollOffset = 0
	}
	maxScroll := len(a.profile.Lines) - a.fgHeight()
	if a.scrollOffset > maxScroll {
		a.scrollOffset = maxScroll
	}
}

func (a *App) moveUp() {
	frame := a.profile.IDStore[a.viewFrameID]
	if frame != nil && frame.Parent != nil {
		a.viewFrameID = frame.Parent.ID
		a.renderer.ViewFrameID = a.viewFrameID
		a.stackScrollOffset = 0
		a.scrollToFrame(frame.Parent)
	}
}

func (a *App) moveDown() {
	frame := a.profile.IDStore[a.viewFrameID]
	if frame == nil {
		return
	}
	var biggest *profile.Frame
	var biggestVal int64
	for _, child := range frame.Children {
		val := int64(0)
		if a.sampleIndex < len(child.Values) {
			val = child.Values[a.sampleIndex]
		}
		if val > biggestVal {
			biggestVal = val
			biggest = child
		}
	}
	if biggest != nil {
		a.viewFrameID = biggest.ID
		a.renderer.ViewFrameID = a.viewFrameID
		a.stackScrollOffset = 0
		a.scrollToFrame(biggest)
	}
}

func (a *App) moveRight() {
	right := a.findRightSibling()
	if right != nil {
		a.viewFrameID = right.ID
		a.renderer.ViewFrameID = a.viewFrameID
		a.stackScrollOffset = 0
		a.scrollToFrame(right)
	}
}

func (a *App) moveLeft() {
	left := a.findLeftSibling()
	if left != nil {
		a.viewFrameID = left.ID
		a.renderer.ViewFrameID = a.viewFrameID
		a.stackScrollOffset = 0
		a.scrollToFrame(left)
	}
}

func (a *App) findRightSibling() *profile.Frame {
	me := a.profile.IDStore[a.viewFrameID]
	if me == nil {
		return nil
	}
	myParent := me.Parent
	for myParent != nil {
		siblings := myParent.Children
		if len(siblings) >= 2 {
			foundMe := false
			for _, s := range siblings {
				if foundMe {
					val := int64(0)
					if a.sampleIndex < len(s.Values) {
						val = s.Values[a.sampleIndex]
					}
					if val > 0 {
						return s
					}
				}
				if s.ID == me.ID {
					foundMe = true
				}
			}
		}
		me = myParent
		myParent = myParent.Parent
	}
	return nil
}

func (a *App) findLeftSibling() *profile.Frame {
	me := a.profile.IDStore[a.viewFrameID]
	if me == nil {
		return nil
	}
	myParent := me.Parent
	for myParent != nil {
		siblings := myParent.Children
		if len(siblings) >= 2 {
			var prev *profile.Frame
			for _, s := range siblings {
				if s.ID == me.ID && prev != nil {
					val := int64(0)
					if a.sampleIndex < len(prev.Values) {
						val = prev.Values[a.sampleIndex]
					}
					if val > 0 {
						return prev
					}
				}
				prev = s
			}
		}
		me = myParent
		myParent = myParent.Parent
	}
	return nil
}

func (a *App) zoomIn() {
	a.focusedStackID = a.viewFrameID
	a.renderer.FocusedStackID = a.focusedStackID
	a.scrollOffset = 0
	a.stackScrollOffset = 0
}

func (a *App) zoomOut() {
	a.focusedStackID = a.profile.RootStack.ID
	a.renderer.FocusedStackID = a.focusedStackID
	a.scrollOffset = 0
	a.stackScrollOffset = 0
}

func (a *App) scrollToFrame(frame *profile.Frame) {
	lineNo := a.profile.FrameIDToLineNo[frame.ID]
	centerLine := max(lineNo-a.fgHeight()/2, 0)
	maxScroll := len(a.profile.Lines) - a.fgHeight()
	if centerLine > maxScroll {
		centerLine = maxScroll
	}
	if centerLine < 0 {
		centerLine = 0
	}
	a.scrollOffset = centerLine
}

func (a *App) headerHeight() int {
	return 1 + 1 // title + tabs
}

func (a *App) footerHeight() int {
	return 10
}

func (a *App) fgHeight() int {
	h := max(a.height-a.headerHeight()-a.footerHeight(), 1)
	return h
}

func (a *App) render() {
	defer func() {
		if rec := recover(); rec != nil {
			// Clear screen and show error
			fmt.Print("\033[2J\033[H")
			fmt.Printf("PANIC in render: %v\r\n", rec)
			fmt.Printf("  width=%d height=%d scrollOffset=%d focusedStack=%d viewFrame=%d sampleIdx=%d\r\n",
				a.width, a.height, a.scrollOffset, a.focusedStackID, a.viewFrameID, a.sampleIndex)
		}
	}()

	if a.showInfoScreen {
		a.renderInfoScreen()
		return
	}

	var sb strings.Builder

	// Clear screen
	sb.WriteString("\033[2J\033[H")

	// Title with sample type
	title := fmt.Sprintf("flametui - %s", a.profile.Filename)
	if a.sampleIndex < len(a.profile.SampleTypes) {
		st := a.profile.SampleTypes[a.sampleIndex]
		title += fmt.Sprintf("  (%s, %s)", st.Type, st.Unit)
	}
	titleBar := centerText(title, a.width)
	sb.WriteString("\033[48;2;46;46;46m\033[1m")
	sb.WriteString(titleBar)
	sb.WriteString(reset)
	sb.WriteString("\r\n")

	// Tabs
	var tabs strings.Builder
	for i, st := range a.profile.SampleTypes {
		tab := fmt.Sprintf(" %s, %s ", st.Type, st.Unit)
		if i == a.sampleIndex {
			tabs.WriteString("\033[48;2;122;122;122m\033[1m")
		} else {
			tabs.WriteString("\033[48;2;90;90;90m")
		}
		tabs.WriteString(tab)
		tabs.WriteString(reset)
	}
	sb.WriteString(tabs.String())
	sb.WriteString("\r\n")

	// Flamegraph
	a.renderer.SetWidth(a.width)
	fgHeight := a.fgHeight()

	startY := max(a.scrollOffset, 0)
	endY := min(startY+fgHeight, len(a.profile.Lines))

	for y := startY; y < endY; y++ {
		line := a.renderer.RenderLine(y, a.width)
		sb.WriteString(line)
		sb.WriteString(reset)
		sb.WriteString("\r\n")
	}

	// Fill remaining flamegraph area
	remaining := fgHeight - (endY - startY)
	if remaining > 0 {
		for range remaining {
			sb.WriteString(strings.Repeat(" ", a.width))
			sb.WriteString("\r\n")
		}
	}

	// Footer
	a.renderFooter(&sb)

	// Write to terminal
	fmt.Print(sb.String())
}

const reset = "\033[0m"

func (a *App) renderFooter(sb *strings.Builder) {
	frame := a.profile.IDStore[a.viewFrameID]
	if frame == nil {
		frame = a.profile.RootStack
	}

	// Frame detail: two stat boxes on left + stack trace on right
	a.renderFrameDetail(sb, frame)

	// Help bar
	sb.WriteString("\033[48;2;46;46;46m")
	sb.WriteString("\033[38;2;170;170;170m")
	hint := " q:quit  h/j/k/l:move  enter:zoom  esc:zoom out  tab:switch sample  i:stack info  mouse:click/move"
	if len(hint) > a.width {
		hint = hint[:a.width]
	} else {
		hint += strings.Repeat(" ", a.width-len(hint))
	}
	sb.WriteString(hint)
	sb.WriteString(reset)
}

func (a *App) renderFrameDetail(sb *strings.Builder, frame *profile.Frame) {
	// Stats
	thisTotal := a.frameThisTotal(frame)
	thisSelf := a.frameSelf(frame)
	allTotal := a.frameAllTotal(frame)
	allSelf := a.frameAllSelf(frame)
	thisTotalPct := a.frameThisTotalPercent(frame)
	thisSelfPct := a.frameThisSelfPercent(frame)
	allTotalPct := a.frameAllTotalPercent(frame)
	allSelfPct := a.frameAllSelfPercent(frame)

	// Layout: left side has two stacked stat boxes, right side has scrollable stack trace
	leftW := 31
	rightW := a.width - leftW - 1
	if rightW < 20 {
		rightW = 20
		leftW = a.width - rightW - 1
	}

	// Box border color
	boxColor := "\033[38;2;204;140;60m"
	boxReset := "\033[39m"

	// visualWidth returns the number of visual columns (runes) in s,
	// ignoring ANSI escape sequences.
	visualWidth := func(s string) int {
		w := 0
		inEscape := false
		for _, r := range s {
			if r == '\033' {
				inEscape = true
				continue
			}
			if inEscape {
				if r == 'm' {
					inEscape = false
				}
				continue
			}
			w++
		}
		return w
	}

	// runePadRight pads or truncates a string to exactly w visual columns (runes).
	runePadRight := func(s string, w int) string {
		rc := visualWidth(s)
		if rc > w {
			// Truncate by visual columns, stripping ANSI
			var result []rune
			col := 0
			inEscape := false
			for _, r := range s {
				if r == '\033' {
					inEscape = true
					result = append(result, r)
					continue
				}
				if inEscape {
					result = append(result, r)
					if r == 'm' {
						inEscape = false
					}
					continue
				}
				if col >= w {
					break
				}
				result = append(result, r)
				col++
			}
			return string(result)
		}
		return s + strings.Repeat(" ", w-rc)
	}

	// makeBoxTop builds "┌─ title ──────────┐" with exact visual width w.
	makeBoxTop := func(title string, w int) string {
		inner := "─ " + title + " "
		rc := utf8.RuneCountInString(inner)
		need := max(
			// 2 for the corners
			w-2-rc, 0)
		return boxColor + "┌" + inner + strings.Repeat("─", need) + "┐" + boxReset
	}

	// makeBoxBottom builds "└──────────────────┘" with exact visual width w.
	makeBoxBottom := func(w int) string {
		return boxColor + "└" + strings.Repeat("─", w-2) + "┘" + boxReset
	}

	// makeBoxLine builds "│ Label: value  pct% │" with exact visual width w.
	makeBoxLine := func(label, val, pct string, w int) string {
		// Layout: "│ " + label + ": " + val + spaces + pct + " │"
		prefix := boxColor + "│ " + boxReset + label + ": "
		prefixW := visualWidth(prefix)
		suffix := boxColor + " │" + boxReset
		suffixW := visualWidth(suffix)
		valW := visualWidth(val)
		pctW := visualWidth(pct)
		// val and pct go to the right side
		spaces := max(w-prefixW-valW-pctW-suffixW, 1)
		return prefix + val + strings.Repeat(" ", spaces) + pct + suffix
	}

	// "This Instance" box (4 lines)
	thisTitle := makeBoxTop("This Instance", leftW)
	thisTotalLine := makeBoxLine("Total", thisTotal, thisTotalPct, leftW)
	thisSelfLine := makeBoxLine("Self", thisSelf, thisSelfPct, leftW)
	thisBottom := makeBoxBottom(leftW)

	// "All Instances" box (4 lines)
	allTitle := makeBoxTop("All Instances", leftW)
	allTotalLine := makeBoxLine("Total", allTotal, allTotalPct, leftW)
	allSelfLine := makeBoxLine("Self", allSelf, allSelfPct, leftW)
	allBottom := makeBoxBottom(leftW)

	// Stack trace lines
	stackLines := frame.RenderDetail(a.sampleIndex, a.profile.SampleTypes[a.sampleIndex].Unit)

	// Fixed footer content area: 8 lines (4 for this + 4 for all)
	contentLines := 8
	stackVisible := max(
		// header + footer line = 6 visible stack lines
		contentLines-2, 0)

	// Clamp stack scroll offset
	maxStackScroll := max(len(stackLines)-stackVisible, 0)
	if a.stackScrollOffset < 0 {
		a.stackScrollOffset = 0
	}
	if a.stackScrollOffset > maxStackScroll {
		a.stackScrollOffset = maxStackScroll
	}

	// Calculate scrollbar thumb position
	showScrollbar := len(stackLines) > stackVisible
	thumbPos := 0
	if showScrollbar && maxStackScroll > 0 {
		thumbPos = int(float64(a.stackScrollOffset) / float64(maxStackScroll) * float64(stackVisible-1))
	}

	// Build stack title with scroll indicator
	titleText := frame.Title()
	if len(stackLines) > stackVisible {
		titleText += fmt.Sprintf(" [%d/%d]", a.stackScrollOffset+1, len(stackLines))
	}
	stackTitle := makeBoxTop(titleText, rightW)
	stackFooter := makeBoxBottom(rightW)

	for i := range contentLines {
		// Left column
		var left string
		switch i {
		case 0:
			left = thisTitle
		case 1:
			left = thisTotalLine
		case 2:
			left = thisSelfLine
		case 3:
			left = thisBottom
		case 4:
			left = allTitle
		case 5:
			left = allTotalLine
		case 6:
			left = allSelfLine
		case 7:
			left = allBottom
		default:
			left = ""
		}

		// Right column: visible portion of stack trace
		var right string
		// Determine right border character (scrollbar thumb or normal border)
		borderChar := "│"
		if showScrollbar && i >= 1 && i < contentLines-1 && (i-1) == thumbPos {
			borderChar = "█"
		}
		if i == 0 {
			right = stackTitle
		} else if i == contentLines-1 {
			right = stackFooter
		} else {
			lineIdx := i - 1 + a.stackScrollOffset
			if lineIdx < len(stackLines) {
				line := boxColor + "│ " + boxReset + stackLines[lineIdx]
				right = runePadRight(line, rightW-1) + boxColor + borderChar + boxReset
			} else {
				right = boxColor + "│" + boxReset + strings.Repeat(" ", rightW-2) + boxColor + borderChar + boxReset
			}
		}

		sb.WriteString(runePadRight(left, leftW))
		sb.WriteString(" ")
		sb.WriteString(runePadRight(right, rightW))
		sb.WriteString("\r\n")
	}
}

func (a *App) renderInfoScreen() {
	var sb strings.Builder
	sb.WriteString("\033[2J\033[H")

	frame := a.profile.IDStore[a.viewFrameID]
	if frame == nil {
		frame = a.profile.RootStack
	}

	title := "Stack Detail Information"
	sb.WriteString("\033[48;2;46;46;46m\033[1m")
	sb.WriteString(centerText(title, a.width))
	sb.WriteString(reset)
	sb.WriteString("\r\n\r\n")

	stackLines := frame.RenderDetail(a.sampleIndex, a.profile.SampleTypes[a.sampleIndex].Unit)
	for _, sl := range stackLines {
		sb.WriteString("  ")
		sb.WriteString(sl)
		sb.WriteString("\r\n")
	}

	sb.WriteString("\r\n\033[38;2;170;170;170m")
	sb.WriteString("Press esc or q to close")
	sb.WriteString(reset)

	fmt.Print(sb.String())
}

func centerText(text string, width int) string {
	if len(text) >= width {
		return text[:width]
	}
	pad := (width - len(text)) / 2
	return strings.Repeat(" ", pad) + text + strings.Repeat(" ", width-pad-len(text))
}

// Stats helpers

func (a *App) frameThisTotal(frame *profile.Frame) string {
	val := int64(0)
	if a.sampleIndex < len(frame.Values) {
		val = frame.Values[a.sampleIndex]
	}
	unit := a.profile.SampleTypes[a.sampleIndex].Unit
	return humanize(unit, val)
}

func (a *App) frameSelf(frame *profile.Frame) string {
	val := int64(0)
	if a.sampleIndex < len(frame.Values) {
		val = frame.Values[a.sampleIndex]
	}
	childVal := int64(0)
	for _, child := range frame.Children {
		if a.sampleIndex < len(child.Values) {
			childVal += child.Values[a.sampleIndex]
		}
	}
	unit := a.profile.SampleTypes[a.sampleIndex].Unit
	return humanize(unit, val-childVal)
}

func (a *App) frameAllTotal(frame *profile.Frame) string {
	frames := a.profile.NameAggr[frame.Name]
	var total int64
	for _, f := range frames {
		if a.sampleIndex < len(f.Values) {
			total += f.Values[a.sampleIndex]
		}
	}
	unit := a.profile.SampleTypes[a.sampleIndex].Unit
	return humanize(unit, total)
}

func (a *App) frameAllSelf(frame *profile.Frame) string {
	frames := a.profile.NameAggr[frame.Name]
	var total int64
	for _, f := range frames {
		val := int64(0)
		if a.sampleIndex < len(f.Values) {
			val = f.Values[a.sampleIndex]
		}
		childVal := int64(0)
		for _, child := range f.Children {
			if a.sampleIndex < len(child.Values) {
				childVal += child.Values[a.sampleIndex]
			}
		}
		total += val - childVal
	}
	unit := a.profile.SampleTypes[a.sampleIndex].Unit
	return humanize(unit, total)
}

func (a *App) frameThisTotalPercent(frame *profile.Frame) string {
	rootVal := int64(0)
	if a.sampleIndex < len(frame.Root.Values) {
		rootVal = frame.Root.Values[a.sampleIndex]
	}
	if rootVal == 0 {
		return "0.00%"
	}
	val := int64(0)
	if a.sampleIndex < len(frame.Values) {
		val = frame.Values[a.sampleIndex]
	}
	return fmt.Sprintf("%.2f%%", float64(val)/float64(rootVal)*100)
}

func (a *App) frameThisSelfPercent(frame *profile.Frame) string {
	rootVal := int64(0)
	if a.sampleIndex < len(frame.Root.Values) {
		rootVal = frame.Root.Values[a.sampleIndex]
	}
	if rootVal == 0 {
		return "0.00%"
	}
	val := int64(0)
	if a.sampleIndex < len(frame.Values) {
		val = frame.Values[a.sampleIndex]
	}
	childVal := int64(0)
	for _, child := range frame.Children {
		if a.sampleIndex < len(child.Values) {
			childVal += child.Values[a.sampleIndex]
		}
	}
	return fmt.Sprintf("%.2f%%", float64(val-childVal)/float64(rootVal)*100)
}

func (a *App) frameAllTotalPercent(frame *profile.Frame) string {
	rootVal := int64(0)
	if a.sampleIndex < len(frame.Root.Values) {
		rootVal = frame.Root.Values[a.sampleIndex]
	}
	if rootVal == 0 {
		return "0.00%"
	}
	frames := a.profile.NameAggr[frame.Name]
	var total int64
	for _, f := range frames {
		if a.sampleIndex < len(f.Values) {
			total += f.Values[a.sampleIndex]
		}
	}
	return fmt.Sprintf("%.2f%%", float64(total)/float64(rootVal)*100)
}

func (a *App) frameAllSelfPercent(frame *profile.Frame) string {
	rootVal := int64(0)
	if a.sampleIndex < len(frame.Root.Values) {
		rootVal = frame.Root.Values[a.sampleIndex]
	}
	if rootVal == 0 {
		return "0.00%"
	}
	frames := a.profile.NameAggr[frame.Name]
	var total int64
	for _, f := range frames {
		val := int64(0)
		if a.sampleIndex < len(f.Values) {
			val = f.Values[a.sampleIndex]
		}
		childVal := int64(0)
		for _, child := range f.Children {
			if a.sampleIndex < len(child.Values) {
				childVal += child.Values[a.sampleIndex]
			}
		}
		total += val - childVal
	}
	return fmt.Sprintf("%.2f%%", float64(total)/float64(rootVal)*100)
}

func humanize(unit string, value int64) string {
	if unit == "bytes" {
		return sizeof(value)
	}
	return fmt.Sprintf("%d", value)
}

func sizeof(num int64) string {
	const unit = 1024
	if num < unit {
		return fmt.Sprintf("%d B", num)
	}
	div, exp := int64(unit), 0
	for n := num / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(num)/float64(div), "KMGTPE"[exp])
}
