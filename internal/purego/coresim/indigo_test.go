package coresim

import "testing"

func TestSimDeviceLegacyClientSendMessageRejectsInvalidClient(t *testing.T) {
	if err := (*SimDeviceLegacyClient)(nil).SendMessage([]byte{1}); err == nil {
		t.Fatal("SendMessage returned nil error for nil client")
	}
	if err := (&SimDeviceLegacyClient{}).SendMessage([]byte{1}); err == nil {
		t.Fatal("SendMessage returned nil error for zero client")
	}
}

func TestSimDeviceLegacyClientSendMessageRejectsEmptyMessage(t *testing.T) {
	client := &SimDeviceLegacyClient{id: 1}
	if err := client.SendMessage(nil); err == nil {
		t.Fatal("SendMessage returned nil error for nil message")
	}
	if err := client.SendMessage([]byte{}); err == nil {
		t.Fatal("SendMessage returned nil error for empty message")
	}
}
