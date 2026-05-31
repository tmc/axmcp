package coresim

import (
	"strings"
	"testing"
)

func TestSimDeviceLegacyClientSendMessageRejectsInvalidClient(t *testing.T) {
	if err := (*SimDeviceLegacyClient)(nil).SendMessage([]byte{1}); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("SendMessage nil client error = %v, want client is nil", err)
	}
	if err := (&SimDeviceLegacyClient{}).SendMessage([]byte{1}); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("SendMessage zero client error = %v, want client is nil", err)
	}
}

func TestSimDeviceLegacyClientSendMessageRejectsEmptyMessage(t *testing.T) {
	client := &SimDeviceLegacyClient{id: 1}
	if err := client.SendMessage(nil); err == nil || !strings.Contains(err.Error(), "message is empty") {
		t.Fatalf("SendMessage nil message error = %v, want message is empty", err)
	}
	if err := client.SendMessage([]byte{}); err == nil || !strings.Contains(err.Error(), "message is empty") {
		t.Fatalf("SendMessage empty message error = %v, want message is empty", err)
	}
}
