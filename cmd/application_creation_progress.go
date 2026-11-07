package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// applicationCreationProgressStep is one entry of the creation_progress list
// the platform returns on an application read (CreationProgressStepResponse in
// the cluster openapi schema).
type applicationCreationProgressStep struct {
	Key     string  `json:"key"`
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

// applicationCreationProgress decodes creation_progress in every shape a
// platform has answered with: a list of steps (current servers), null (no
// analysis has recorded progress yet), or a plain sentence (older servers).
type applicationCreationProgress struct {
	Steps []applicationCreationProgressStep
	Text  string
}

func (progress *applicationCreationProgress) UnmarshalJSON(payload []byte) error {
	trimmed := bytes.TrimSpace(payload)
	*progress = applicationCreationProgress{}
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '"':
		return json.Unmarshal(trimmed, &progress.Text)
	case '[':
		return json.Unmarshal(trimmed, &progress.Steps)
	default:
		return fmt.Errorf("creation_progress must be a list of steps, a string or null, got %s", trimmed)
	}
}

// creationProgressStatusActive and creationProgressStatusCompleted are the
// step statuses the platform's analysis writes.
const (
	creationProgressStatusActive    = "active"
	creationProgressStatusCompleted = "completed"
)

// Summary is the one line --wait prints for the progress: the newest step,
// worded for a person, or the legacy sentence as the server sent it.
func (progress applicationCreationProgress) Summary() string {
	if len(progress.Steps) == 0 {
		return strings.TrimSpace(progress.Text)
	}
	latest := progress.Steps[len(progress.Steps)-1]
	label := creationProgressStepLabel(latest.Key)
	if label == "" {
		return ""
	}
	message := ""
	if latest.Message != nil {
		message = strings.TrimSpace(*latest.Message)
	}
	switch strings.TrimSpace(latest.Status) {
	case creationProgressStatusActive:
		return label + "..."
	case creationProgressStatusCompleted:
		if message != "" {
			return label + ": " + message
		}
		return label + ": done"
	}
	if message != "" {
		return label + ": " + message
	}
	return label
}

// creationProgressWordSpelling spells the step-key words a plain capitalise
// would get wrong.
var creationProgressWordSpelling = map[string]string{
	"ai":     "AI",
	"github": "GitHub",
	"repo":   "repository",
}

// creationProgressStepLabel turns a step key such as "fetch_repo_tree" into
// "Fetch repository tree".
func creationProgressStepLabel(key string) string {
	words := strings.Fields(strings.ReplaceAll(strings.TrimSpace(key), "_", " "))
	for index, word := range words {
		if spelling, isKnown := creationProgressWordSpelling[strings.ToLower(word)]; isKnown {
			words[index] = spelling
		}
	}
	if len(words) == 0 {
		return ""
	}
	first := words[0]
	words[0] = strings.ToUpper(first[:1]) + first[1:]
	return strings.Join(words, " ")
}
