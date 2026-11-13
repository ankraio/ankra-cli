package cmd

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// The onboarding banner. Terminals that can show pictures get the real Ankra
// lockup, the symbol beside the ANKRA wordmark, as an image; every other
// terminal gets the name letter-spaced in capitals, as the wordmark is. Drawing the lockup out of text
// characters was tried (braille, half and quadrant blocks) and at a size that
// fits a terminal it always came out jagged.
//
// The two PNGs are the brand artwork (https://ankra.ai/brand/), trimmed and
// scaled to 200 pixels high. The logo is monochrome by the brand rules, so
// there is a white one for dark terminals and a black one for light ones.

//go:embed onboard_logo/ankra-lockup-white.png
var onboardLockupWhite []byte

//go:embed onboard_logo/ankra-lockup-black.png
var onboardLockupBlack []byte

// onboardLogoRows is how many text rows the lockup image is scaled to; the
// terminal works out the width from the picture's proportions.
const onboardLogoRows = 5

// onboardTextMark is the banner for terminals that cannot show the image. A
// hexagon glyph was tried in front of it; terminal fonts draw ⬡ and its
// relatives as a dot-sized ring that reads as a bullet, not a logo.
const onboardTextMark = "A N K R A"

// The inline image protocols the banner speaks.
const (
	onboardImageNone  = ""
	onboardImageITerm = "iterm" // OSC 1337, WezTerm
	onboardImageKitty = "kitty" // the kitty graphics protocol, kitty and Ghostty
)

// onboardImageProtocol names the image protocol the terminal is known to
// support, read from the environment it sets. It is an allowlist on purpose: a
// terminal that does not understand the escape prints nothing at all, so a
// terminal is only sent a picture when it is known to draw it. VS Code and
// Windows Terminal are left out (VS Code draws images only with a setting most
// people have off; Windows Terminal reads sixel, and only from 1.22), and so is
// tmux or screen, which pass neither protocol through by default. iTerm2 is
// left out too: iTerm2 3.7.7 answered the real onboarding with an "Allow
// Terminal-Initiated Display?" dialog about an "Unnamed file" instead of the
// picture, and a security prompt is a worse first sight than the text.
func onboardImageProtocol(getenv func(string) string) string {
	if getenv("TMUX") != "" || getenv("STY") != "" || getenv("TERM") == "dumb" {
		return onboardImageNone
	}
	switch getenv("TERM_PROGRAM") {
	case "WezTerm":
		return onboardImageITerm
	case "ghostty":
		return onboardImageKitty
	}
	switch getenv("TERM") {
	case "xterm-kitty", "xterm-ghostty":
		return onboardImageKitty
	}
	if getenv("KITTY_WINDOW_ID") != "" {
		return onboardImageKitty
	}
	return onboardImageNone
}

// onboardKittyChunk is the most base64 one kitty graphics escape may carry.
const onboardKittyChunk = 4096

// onboardInlineImage is the escape sequence that draws png rows text rows
// high, keeping its proportions. It leaves the cursor on the image's last row,
// so the caller ends the line.
func onboardInlineImage(protocol string, png []byte, rows int) string {
	encoded := base64.StdEncoding.EncodeToString(png)
	switch protocol {
	case onboardImageITerm:
		return fmt.Sprintf("\x1b]1337;File=inline=1;size=%d;height=%d;preserveAspectRatio=1:%s\a", len(png), rows, encoded)
	case onboardImageKitty:
		// a=T transmits and shows the image, f=100 says it is a PNG, q=2
		// stops the terminal from answering on stdin, where the answer
		// would land in the next prompt.
		var sequence strings.Builder
		for start := 0; start < len(encoded); start += onboardKittyChunk {
			end := min(start+onboardKittyChunk, len(encoded))
			more := 0
			if end < len(encoded) {
				more = 1
			}
			if start == 0 {
				fmt.Fprintf(&sequence, "\x1b_Ga=T,f=100,q=2,r=%d,m=%d;%s\x1b\\", rows, more, encoded[start:end])
			} else {
				fmt.Fprintf(&sequence, "\x1b_Gm=%d;%s\x1b\\", more, encoded[start:end])
			}
		}
		return sequence.String()
	}
	return ""
}

// printBanner prints the logo and the tagline.
func (run *onboardRun) printBanner() {
	protocol := onboardImageProtocol(os.Getenv)
	if protocol != onboardImageNone {
		logo := onboardLockupWhite
		if dark, known := onboardDarkBackground(os.Getenv, run.cmd.InOrStdin(), run.out); known && !dark {
			logo = onboardLockupBlack
		}
		_, _ = io.WriteString(run.out, onboardInlineImage(protocol, logo, onboardLogoRows))
		_, _ = fmt.Fprintln(run.out)
		_, _ = fmt.Fprintln(run.out)
		_, _ = fmt.Fprintln(run.out, run.style.bold(onboardTagline))
		return
	}
	_, _ = fmt.Fprintln(run.out, "  "+run.style.bold(onboardTextMark))
	_, _ = fmt.Fprintln(run.out, "  "+onboardTagline)
}

// onboardDarkBackground reports whether the terminal's background is dark,
// and whether that could be found out at all. COLORFGBG answers it when the
// terminal sets it; otherwise the terminal is asked (onboardQueryBackground),
// but only when both streams are that terminal.
func onboardDarkBackground(getenv func(string) string, input io.Reader, output io.Writer) (dark, known bool) {
	if dark, known := onboardColorFgBgIsDark(getenv("COLORFGBG")); known {
		return dark, true
	}
	inputFile, isInputFile := input.(*os.File)
	outputFile, isOutputFile := output.(*os.File)
	if !isInputFile || !isOutputFile || !term.IsTerminal(int(inputFile.Fd())) || !term.IsTerminal(int(outputFile.Fd())) {
		return false, false
	}
	return onboardParseBackground(onboardQueryBackground(inputFile, outputFile))
}

// onboardColorFgBgIsDark reads COLORFGBG ("15;0", or "15;default;0"): the last
// field is the background's colour number, and 7 and 9 to 15 are the light ones.
func onboardColorFgBgIsDark(value string) (dark, known bool) {
	if value == "" {
		return false, false
	}
	fields := strings.Split(value, ";")
	background, parseError := strconv.Atoi(fields[len(fields)-1])
	if parseError != nil || background < 0 || background > 15 {
		return false, false
	}
	return background != 7 && background < 9, true
}

// onboardBackgroundReply matches a terminal's answer to OSC 11, the
// background colour, as rgb:R/G/B with one to four hex digits a channel.
var onboardBackgroundReply = regexp.MustCompile(`\x1b\]11;rgba?:([0-9A-Fa-f]{1,4})/([0-9A-Fa-f]{1,4})/([0-9A-Fa-f]{1,4})`)

// onboardDeviceAttributesReply matches the answer to DA1, which every terminal
// sends; asking it after OSC 11 tells when to stop waiting.
var onboardDeviceAttributesReply = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// onboardParseBackground reads the background colour out of the terminal's
// reply and says whether it is dark.
func onboardParseBackground(reply []byte) (dark, known bool) {
	match := onboardBackgroundReply.FindSubmatch(reply)
	if match == nil {
		return false, false
	}
	channel := func(hex []byte) float64 {
		value, _ := strconv.ParseUint(string(hex), 16, 16)
		return float64(value) / float64(uint64(1)<<(4*len(hex))-1)
	}
	luminance := 0.2126*channel(match[1]) + 0.7152*channel(match[2]) + 0.0722*channel(match[3])
	return luminance < 0.5, true
}
