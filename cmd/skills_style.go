package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// skillsStyle renders the human output of the `ankra skills` family. Colour
// is used only when stdout is a terminal and neither NO_COLOR nor TERM=dumb
// asks otherwise, so piped output, the `ankra upgrade` refresh and the tests
// all see plain text. Width is the terminal width, or zero when unknown, in
// which case nothing is truncated.
type skillsStyle struct {
	isColor bool
	width   int
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

func newSkillsStyle(writer io.Writer) skillsStyle {
	file, isFile := writer.(*os.File)
	if !isFile || !term.IsTerminal(int(file.Fd())) {
		return skillsStyle{}
	}
	style := skillsStyle{
		isColor: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
	}
	if columns, _, sizeError := term.GetSize(int(file.Fd())); sizeError == nil && columns > 0 {
		style.width = columns
	}
	return style
}

func (style skillsStyle) paint(code, text string) string {
	if !style.isColor || text == "" {
		return text
	}
	return code + text + ansiReset
}

func (style skillsStyle) bold(text string) string   { return style.paint(ansiBold, text) }
func (style skillsStyle) dim(text string) string    { return style.paint(ansiDim, text) }
func (style skillsStyle) green(text string) string  { return style.paint(ansiGreen, text) }
func (style skillsStyle) yellow(text string) string { return style.paint(ansiYellow, text) }
func (style skillsStyle) cyan(text string) string   { return style.paint(ansiCyan, text) }

// skillsStepKind is the outcome of one install or uninstall step, which
// decides its marker.
type skillsStepKind int

const (
	skillsStepDone skillsStepKind = iota
	skillsStepSkipped
	skillsStepWarning
	skillsStepNote
)

// step prints one aligned line of a per-assistant checklist:
//
//	✓ skills     25 installed
//	! hook       not supported by Codex CLI; skipped
func (style skillsStyle) step(writer io.Writer, kind skillsStepKind, label, detail string) {
	var marker string
	switch kind {
	case skillsStepDone:
		marker = style.green("✓")
	case skillsStepSkipped:
		marker = style.yellow("–")
	case skillsStepWarning:
		marker = style.yellow("!")
	default:
		marker = style.cyan("i")
	}
	_, _ = fmt.Fprintf(writer, "  %s %s %s\n", marker, style.dim(padRight(label, 10)), detail)
}

// heading prints the line that opens one assistant's block.
func (style skillsStyle) heading(writer io.Writer, title, scope, directory string) {
	_, _ = fmt.Fprintf(writer, "%s  %s\n", style.bold(title), style.dim(scope+" · "+directory))
}

// hint prints a dimmed follow-up line, such as the next command to run.
func (style skillsStyle) hint(writer io.Writer, format string, arguments ...interface{}) {
	_, _ = fmt.Fprintln(writer, style.dim(fmt.Sprintf(format, arguments...)))
}

// truncateToWidth shortens text to fit width display columns, ending in an
// ellipsis. A width of zero or less leaves the text whole.
func truncateToWidth(text string, width int) string {
	if width <= 0 || utf8.RuneCountInString(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	return strings.TrimRight(string(runes[:width-1]), " ,;:-") + "…"
}

func padRight(text string, width int) string {
	length := utf8.RuneCountInString(text)
	if length >= width {
		return text
	}
	return text + strings.Repeat(" ", width-length)
}

// skillsTableCell is one cell of an aligned table: plain is measured,
// styled is printed, so colour codes never skew the columns.
type skillsTableCell struct {
	plain  string
	styled string
}

func plainCell(text string) skillsTableCell { return skillsTableCell{plain: text, styled: text} }

// writeSkillsTable prints rows under a dimmed header with two spaces between
// columns. The last column is not padded and is truncated to the terminal
// width when one is known.
func (style skillsStyle) writeSkillsTable(writer io.Writer, header []string, rows [][]skillsTableCell) {
	widths := make([]int, len(header))
	for index, title := range header {
		widths[index] = utf8.RuneCountInString(title)
	}
	for _, row := range rows {
		for index, cell := range row {
			if length := utf8.RuneCountInString(cell.plain); length > widths[index] {
				widths[index] = length
			}
		}
	}
	leading := 0
	for index := 0; index < len(widths)-1; index++ {
		leading += widths[index] + 2
	}
	lastWidth := 0
	if style.width > 0 {
		lastWidth = style.width - leading
		if lastWidth < 20 {
			lastWidth = 0
		}
	}

	headerCells := make([]skillsTableCell, len(header))
	for index, title := range header {
		headerCells[index] = skillsTableCell{plain: title, styled: style.dim(title)}
	}
	for _, row := range append([][]skillsTableCell{headerCells}, rows...) {
		var line strings.Builder
		for index, cell := range row {
			if index == len(row)-1 {
				if lastWidth > 0 && cell.plain == cell.styled {
					line.WriteString(truncateToWidth(cell.plain, lastWidth))
				} else {
					line.WriteString(cell.styled)
				}
				continue
			}
			line.WriteString(cell.styled)
			line.WriteString(strings.Repeat(" ", widths[index]-utf8.RuneCountInString(cell.plain)+2))
		}
		_, _ = fmt.Fprintln(writer, strings.TrimRight(line.String(), " "))
	}
}

// skillSummary is the first clause of a skill description: the skill
// descriptions open with what the skill does, then a " - " or a sentence
// break before the detail and the "Use when ..." trigger list.
func skillSummary(description string) string {
	summary := strings.TrimSpace(description)
	for _, separator := range []string{" - ", " — ", ". "} {
		if index := strings.Index(summary, separator); index > 0 {
			summary = summary[:index]
		}
	}
	return strings.TrimSuffix(summary, ".")
}

// pluralize returns "1 skill" / "3 skills".
func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

// writeSkillsList renders 'ankra skills list': one line per skill with its
// summary, or the whole description under each name with --long.
func writeSkillsList(writer io.Writer, entries []skillListEntry, isLong bool) {
	style := newSkillsStyle(writer)
	installedCount := 0
	for _, entry := range entries {
		if entry.Installed {
			installedCount++
		}
	}
	if isLong {
		for index, entry := range entries {
			if index > 0 {
				_, _ = fmt.Fprintln(writer)
			}
			_, _ = fmt.Fprintf(writer, "%s %s\n", skillInstalledMarker(style, entry.Installed), style.bold(entry.Name))
			_, _ = fmt.Fprintf(writer, "  %s\n", entry.Description)
		}
	} else {
		rows := make([][]skillsTableCell, 0, len(entries))
		for _, entry := range entries {
			rows = append(rows, []skillsTableCell{
				{plain: plainMarker(entry.Installed), styled: skillInstalledMarker(style, entry.Installed)},
				{plain: entry.Name, styled: style.bold(entry.Name)},
				plainCell(skillSummary(entry.Description)),
			})
		}
		style.writeSkillsTable(writer, []string{" ", "SKILL", "WHAT IT COVERS"}, rows)
	}
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintf(writer, "%s · %s installed\n",
		pluralize(len(entries), "skill", "skills"), style.green(fmt.Sprintf("%d", installedCount)))
	switch {
	case installedCount < len(entries):
		style.hint(writer, "Install with: ankra skills install    Details: ankra skills list --long")
	case !isLong:
		style.hint(writer, "Details: ankra skills list --long")
	}
}

func plainMarker(isInstalled bool) string {
	if isInstalled {
		return "●"
	}
	return "○"
}

func skillInstalledMarker(style skillsStyle, isInstalled bool) string {
	if isInstalled {
		return style.green("●")
	}
	return style.dim("○")
}

// writeClientsTable renders 'ankra skills clients' as an aligned table with
// the install state first, so the assistants that matter stand out.
func writeClientsTable(writer io.Writer, entries []clientListEntry) {
	style := newSkillsStyle(writer)
	rows := make([][]skillsTableCell, 0, len(entries))
	for _, entry := range entries {
		var status skillsTableCell
		switch {
		case entry.Installed:
			status = skillsTableCell{plain: "● installed", styled: style.green("● installed")}
		case entry.Detected:
			status = skillsTableCell{plain: "○ detected", styled: style.cyan("○ detected")}
		default:
			status = skillsTableCell{plain: "·", styled: style.dim("·")}
		}
		rows = append(rows, []skillsTableCell{
			status,
			{plain: entry.ID, styled: style.bold(entry.ID)},
			plainCell(entry.DisplayName),
			plainCell(entry.Directory),
			{plain: entry.Delivery, styled: style.dim(entry.Delivery)},
		})
	}
	style.writeSkillsTable(writer, []string{"STATUS", "CLIENT", "ASSISTANT", "INSTALLS TO", "DELIVERY"}, rows)
	_, _ = fmt.Fprintln(writer)
	style.hint(writer, "Install with: ankra skills install --client <client> (or --client all)")
}
