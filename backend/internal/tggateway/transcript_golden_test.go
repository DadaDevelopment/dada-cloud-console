package tggateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenCase mirrors agentkit/transcript_golden.json, the single description of what the
// runtime receives from a group chat. The python side reads the same file in
// agentkit/tests/test_transcript.py, so a format change landing on one side only turns the
// other side red instead of quietly splitting eval input from production input.
type goldenCase struct {
	Name            string `json:"name"`
	Text            string `json:"text"`
	FirstName       string `json:"first_name"`
	Username        string `json:"username"`
	QuotedText      string `json:"quoted_text"`
	QuotedIsChannel bool   `json:"quoted_is_channel"`
	QuotedUsername  string `json:"quoted_username"`
	IsChannelPost   bool   `json:"is_channel_post"`

	MediaKind        string `json:"media_kind"`
	MediaDescription string `json:"media_description"`
	MediaTranscript  string `json:"media_transcript"`
	MediaFileName    string `json:"media_file_name"`

	WantSpeaker string `json:"want_speaker"`
	WantQuoted  string `json:"want_quoted"`
	WantMedia   string `json:"want_media"`
	WantInbound string `json:"want_inbound"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	path := filepath.Join("..", "..", "..", "agentkit", "transcript_golden.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Cases) == 0 {
		t.Fatalf("%s carries no cases", path)
	}
	return doc.Cases
}

func TestInboundContent_MatchesTheSharedGolden(t *testing.T) {
	for _, c := range loadGolden(t) {
		u := TelegramUpdate{
			ChatType:           "supergroup",
			Text:               c.Text,
			FirstName:          c.FirstName,
			Username:           c.Username,
			ReplyToText:        c.QuotedText,
			ReplyToIsChannel:   c.QuotedIsChannel,
			ReplyToUsername:    c.QuotedUsername,
			IsAutomaticForward: c.IsChannelPost,
			FromIsBot:          c.IsChannelPost,
		}
		if c.MediaKind != "" {
			u.Attachment = &TelegramAttachment{
				Kind:        c.MediaKind,
				Description: c.MediaDescription,
				Transcript:  c.MediaTranscript,
				FileName:    c.MediaFileName,
			}
		}
		if got := GroupSpeaker(u); got != c.WantSpeaker {
			t.Errorf("%s: speaker = %q, want %q", c.Name, got, c.WantSpeaker)
		}
		if got := QuotedContext(u); got != c.WantQuoted {
			t.Errorf("%s: quoted = %q, want %q", c.Name, got, c.WantQuoted)
		}
		if got := MediaContext(u.Attachment); got != c.WantMedia {
			t.Errorf("%s: media = %q, want %q", c.Name, got, c.WantMedia)
		}
		if got := InboundContent(u); got != c.WantInbound {
			t.Errorf("%s: inbound = %q, want %q", c.Name, got, c.WantInbound)
		}
	}
}

func TestInboundContent_PrivateChatStaysBare(t *testing.T) {
	u := TelegramUpdate{ChatType: "private", Text: "привет", FirstName: "Игорь", ReplyToText: "что-то"}
	if got := InboundContent(u); got != "привет" {
		t.Fatalf("private inbound = %q, want the bare text", got)
	}
}
