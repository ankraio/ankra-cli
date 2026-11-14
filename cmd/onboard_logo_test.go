package cmd

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestOnboardImageProtocolOnlyForTerminalsKnownToDrawImages(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"iTerm2, which asks before it shows the picture", map[string]string{"TERM_PROGRAM": "iTerm.app", "LC_TERMINAL": "iTerm2"}, onboardImageNone},
		{"WezTerm", map[string]string{"TERM_PROGRAM": "WezTerm"}, onboardImageITerm},
		{"Ghostty", map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"}, onboardImageKitty},
		{"Ghostty over ssh", map[string]string{"TERM": "xterm-ghostty"}, onboardImageKitty},
		{"kitty", map[string]string{"TERM": "xterm-kitty", "KITTY_WINDOW_ID": "1"}, onboardImageKitty},
		{"macOS Terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, onboardImageNone},
		{"VS Code", map[string]string{"TERM_PROGRAM": "vscode"}, onboardImageNone},
		{"Windows Terminal", map[string]string{"WT_SESSION": "x"}, onboardImageNone},
		{"GNOME Terminal", map[string]string{"TERM": "xterm-256color", "VTE_VERSION": "7600"}, onboardImageNone},
		{"WezTerm inside tmux", map[string]string{"TERM_PROGRAM": "WezTerm", "TMUX": "/tmp/tmux-501/default,1,0"}, onboardImageNone},
		{"kitty inside screen", map[string]string{"KITTY_WINDOW_ID": "1", "STY": "1.pts-0"}, onboardImageNone},
		{"dumb terminal", map[string]string{"TERM_PROGRAM": "WezTerm", "TERM": "dumb"}, onboardImageNone},
		{"nothing set", map[string]string{}, onboardImageNone},
	}
	for _, testCase := range cases {
		getenv := func(key string) string { return testCase.env[key] }
		if got := onboardImageProtocol(getenv); got != testCase.want {
			t.Errorf("%s: protocol %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestOnboardInlineImageITermCarriesThePNG(t *testing.T) {
	sequence := onboardInlineImage(onboardImageITerm, onboardLockupWhite, 5)
	prefix := "\x1b]1337;File=inline=1;size=" + strconv.Itoa(len(onboardLockupWhite)) + ";height=5;preserveAspectRatio=1:"
	if !strings.HasPrefix(sequence, prefix) || !strings.HasSuffix(sequence, "\a") {
		t.Fatalf("sequence does not have the OSC 1337 frame: %q...", sequence[:min(len(sequence), 80)])
	}
	decoded, decodeError := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(sequence, prefix), "\a"))
	if decodeError != nil || !bytes.Equal(decoded, onboardLockupWhite) {
		t.Fatalf("payload is not the PNG (decode error %v)", decodeError)
	}
}

func TestOnboardInlineImageKittyChunksThePNG(t *testing.T) {
	sequence := onboardInlineImage(onboardImageKitty, onboardLockupBlack, 5)
	escapes := regexp.MustCompile(`\x1b_G([^;]*);([^\x1b]*)\x1b\\`).FindAllStringSubmatch(sequence, -1)
	if len(escapes) < 2 {
		t.Fatalf("got %d escapes, want the PNG split over several", len(escapes))
	}
	if joined := strings.Join(regexp.MustCompile(`\x1b_G[^;]*;[^\x1b]*\x1b\\`).FindAllString(sequence, -1), ""); joined != sequence {
		t.Fatalf("sequence carries bytes outside the graphics escapes")
	}
	var payload strings.Builder
	for index, escape := range escapes {
		control, chunk := escape[1], escape[2]
		if len(chunk) > onboardKittyChunk {
			t.Errorf("chunk %d carries %d bytes, more than %d", index, len(chunk), onboardKittyChunk)
		}
		wantMore := "m=1"
		if index == len(escapes)-1 {
			wantMore = "m=0"
		}
		if index == 0 {
			if want := "a=T,f=100,q=2,r=5," + wantMore; control != want {
				t.Errorf("first control %q, want %q", control, want)
			}
		} else if control != wantMore {
			t.Errorf("chunk %d control %q, want %q", index, control, wantMore)
		}
		payload.WriteString(chunk)
	}
	decoded, decodeError := base64.StdEncoding.DecodeString(payload.String())
	if decodeError != nil || !bytes.Equal(decoded, onboardLockupBlack) {
		t.Fatalf("payload is not the PNG (decode error %v)", decodeError)
	}
	if onboardInlineImage(onboardImageNone, onboardLockupBlack, 5) != "" {
		t.Fatalf("no protocol must print nothing")
	}
}

func TestOnboardLockupImagesAreTheWideLockup(t *testing.T) {
	for name, image := range map[string][]byte{"white": onboardLockupWhite, "black": onboardLockupBlack} {
		config, decodeError := png.DecodeConfig(bytes.NewReader(image))
		if decodeError != nil {
			t.Fatalf("%s: not a PNG: %v", name, decodeError)
		}
		if ratio := float64(config.Width) / float64(config.Height); ratio < 3 || ratio > 4.5 {
			t.Errorf("%s: %dx%d is not the horizontal lockup's shape", name, config.Width, config.Height)
		}
		if len(image) > 64*1024 {
			t.Errorf("%s: %d bytes is a lot to send to a terminal", name, len(image))
		}
	}
}

func TestOnboardParseBackground(t *testing.T) {
	cases := []struct {
		reply       string
		dark, known bool
	}{
		{"\x1b]11;rgb:12b6/14d7/1d57\x1b\\\x1b[?64;1;2;4;6;17;18;21;22;52c", true, true}, // iTerm2, dark
		{"\x1b]11;rgb:2828/2c2c/3434\x1b\\\x1b[?62;22;52c", true, true},                  // Ghostty, dark
		{"\x1b]11;rgb:ffff/ffff/ffff\a\x1b[?62c", false, true},                           // white, BEL-terminated
		{"\x1b]11;rgb:fd/f6/e3\x1b\\", false, true},                                      // Solarized light, two digits
		{"\x1b]11;rgba:0000/0000/0000/ffff\x1b\\", true, true},
		{"\x1b[?62;22c", false, false}, // answered DA1 only
		{"", false, false},
		{"\x1b]11;garbage\x1b\\", false, false},
	}
	for _, testCase := range cases {
		dark, known := onboardParseBackground([]byte(testCase.reply))
		if dark != testCase.dark || known != testCase.known {
			t.Errorf("%q: dark=%v known=%v, want dark=%v known=%v", testCase.reply, dark, known, testCase.dark, testCase.known)
		}
	}
	if !onboardDeviceAttributesReply.MatchString("\x1b]11;rgb:0/0/0\x1b\\\x1b[?62;22;52c") {
		t.Errorf("the DA1 answer must end the wait")
	}
}

func TestOnboardColorFgBgIsDark(t *testing.T) {
	cases := []struct {
		value       string
		dark, known bool
	}{
		{"15;0", true, true},
		{"0;15", false, true},
		{"0;7", false, true},
		{"15;default;0", true, true},
		{"7;8", true, true},
		{"", false, false},
		{"15;default", false, false},
		{"15;99", false, false},
	}
	for _, testCase := range cases {
		dark, known := onboardColorFgBgIsDark(testCase.value)
		if dark != testCase.dark || known != testCase.known {
			t.Errorf("%q: dark=%v known=%v, want dark=%v known=%v", testCase.value, dark, known, testCase.dark, testCase.known)
		}
	}
}

func TestOnboardDarkBackgroundDoesNotAskWithoutATerminal(t *testing.T) {
	var output bytes.Buffer
	dark, known := onboardDarkBackground(func(string) string { return "" }, strings.NewReader(""), &output)
	if known || dark {
		t.Fatalf("dark=%v known=%v without a terminal, want unknown", dark, known)
	}
	if output.Len() != 0 {
		t.Fatalf("wrote %q to a stream that is not a terminal", output.String())
	}
	if dark, known := onboardDarkBackground(func(key string) string {
		if key == "COLORFGBG" {
			return "0;15"
		}
		return ""
	}, strings.NewReader(""), &output); !known || dark {
		t.Fatalf("COLORFGBG light: dark=%v known=%v", dark, known)
	}
}
