package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestNewClientUsesDMIntentsOnlyAndNoStateCache(t *testing.T) {
	client, err := NewClient("token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.session.Identify.Intents != discordgo.IntentsDirectMessages {
		t.Fatalf("intents=%d", client.session.Identify.Intents)
	}
	if client.session.StateEnabled || client.session.State.MaxMessageCount != 0 {
		t.Fatal("the SDK cache would retain message bodies")
	}
	if _, err := NewClient("", nil); err == nil {
		t.Fatal("an empty token must be refused")
	}
	// Close before Open is safe and idempotent.
	_ = client.Close()
	_ = client.Close()
}

func TestInboundMapsTheSDKMessage(t *testing.T) {
	message := &discordgo.Message{
		ID: "1", ChannelID: "c", GuildID: "", WebhookID: "", Content: "hi",
		Author:            &discordgo.User{ID: "u", Bot: false},
		Attachments:       []*discordgo.MessageAttachment{{}},
		ReferencedMessage: &discordgo.Message{Content: "prev", Author: &discordgo.User{ID: "b", Bot: true}},
	}
	in := inbound(message)
	if in.MessageID != "1" || in.ChannelID != "c" || in.AuthorID != "u" || in.Attachments != 1 || in.Reference == nil || !in.Reference.AuthorIsBot || in.Reference.Content != "prev" {
		t.Fatalf("in=%+v", in)
	}
	if noMentions.Parse == nil || len(noMentions.Parse) != 0 {
		t.Fatal("mention parsing must be explicitly disabled")
	}
}
