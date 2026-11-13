//go:build darwin || linux

package cmd

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// onboardBackgroundTimeout bounds the wait for the terminal's answer. The
// terminals the image goes to answer in well under 50ms.
const onboardBackgroundTimeout = 400 * time.Millisecond

// onboardQueryBackground asks the terminal for its background colour (OSC 11)
// and then for its device attributes (DA1), and returns what came back. Every
// terminal answers DA1, so its answer ends the wait; the deadline covers one
// that does not. The reads wait in select(2) rather than in a goroutine: a
// read left blocked after giving up would swallow the first key of the next
// prompt. The terminal is in raw mode for the exchange, so the answers are
// neither echoed nor held back until a newline.
func onboardQueryBackground(input, output *os.File) []byte {
	descriptor := int(input.Fd())
	state, rawError := term.MakeRaw(descriptor)
	if rawError != nil {
		return nil
	}
	defer func() { _ = term.Restore(descriptor, state) }()
	if _, writeError := output.WriteString("\x1b]11;?\x1b\\\x1b[c"); writeError != nil {
		return nil
	}
	var reply []byte
	buffer := make([]byte, 256)
	deadline := time.Now().Add(onboardBackgroundTimeout)
	for !onboardDeviceAttributesReply.Match(reply) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		var readable unix.FdSet
		readable.Set(descriptor)
		timeout := unix.NsecToTimeval(remaining.Nanoseconds())
		ready, selectError := unix.Select(descriptor+1, &readable, nil, nil, &timeout)
		if errors.Is(selectError, unix.EINTR) {
			continue
		}
		if selectError != nil || ready == 0 {
			break
		}
		count, readError := unix.Read(descriptor, buffer)
		if readError != nil || count <= 0 {
			break
		}
		reply = append(reply, buffer[:count]...)
	}
	return reply
}
