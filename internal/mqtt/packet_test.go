package mqtt

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEncodePublishRetained(t *testing.T) {
	t.Parallel()

	got, err := encodePublish(Message{Topic: "casa/estado", Payload: []byte("online"), Retain: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x31, 0x13, 0x00, 0x0b, 'c', 'a', 's', 'a', '/', 'e', 's', 't', 'a', 'd', 'o', 'o', 'n', 'l', 'i', 'n', 'e'}
	if !bytes.Equal(got, want) {
		t.Fatalf("PUBLISH = %x; quiere %x", got, want)
	}
}

func TestReadPacketRejectsFiveByteRemainingLength(t *testing.T) {
	t.Parallel()

	_, err := readPacket(bufio.NewReader(bytes.NewReader([]byte{0x30, 0x80, 0x80, 0x80, 0x80, 0x00})))
	if err == nil || !strings.Contains(err.Error(), "remaining length") {
		t.Fatalf("error = %v; quiere remaining length inválido", err)
	}
}

func TestReadPacketRejectsNonCanonicalRemainingLength(t *testing.T) {
	t.Parallel()

	_, err := readPacket(bufio.NewReader(bytes.NewReader([]byte{0xd0, 0x80, 0x00})))
	if err == nil || !strings.Contains(err.Error(), "mal formado") {
		t.Fatalf("error = %v; quiere remaining length mal formado", err)
	}
}

func TestReadPacketRejectsPacketLargerThanLimit(t *testing.T) {
	t.Parallel()

	data := append([]byte{0x30, 0xfd, 0xff, 0x03}, make([]byte, maxPacketSize-1)...)
	_, err := readPacket(bufio.NewReader(bytes.NewReader(data)))
	if !errors.Is(err, errPacketTooLarge) {
		t.Fatalf("error = %v; quiere errPacketTooLarge", err)
	}
}

func TestTopicValidation(t *testing.T) {
	t.Parallel()

	for _, topic := range []string{"", "a/+", "a/#", "a\x00b", string([]byte{0xff}), strings.Repeat("a", 65536)} {
		if err := validateTopic(topic); err == nil {
			t.Errorf("validateTopic(%q) no devolvió error", topic)
		}
	}
	for _, topic := range []string{"a", "/", "cámara/entrada", strings.Repeat("a", 65535)} {
		if err := validateTopic(topic); err != nil {
			t.Errorf("validateTopic(%q): %v", topic, err)
		}
	}
}

func TestSubscriptionFilterValidation(t *testing.T) {
	t.Parallel()

	for _, filter := range []string{"casa/+/estado", "casa/#", "+/estado", "#"} {
		if err := validateFilter(filter); err != nil {
			t.Errorf("validateFilter(%q): %v", filter, err)
		}
	}
	for _, filter := range []string{"", "casa+", "casa/#/estado", "casa/+estado", "a\x00b"} {
		if err := validateFilter(filter); err == nil {
			t.Errorf("validateFilter(%q) no devolvió error", filter)
		}
	}
}

func TestFilterMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		topic  string
		want   bool
	}{
		{"casa/+/estado", "casa/cocina/estado", true},
		{"casa/#", "casa", true},
		{"casa/#", "casa/cocina/temperatura", true},
		{"+/estado", "cocina/estado", true},
		{"casa/+/estado", "casa/estado", false},
		{"casa/+/estado", "casa/cocina/luz", false},
		{"#", "$SYS/broker/uptime", false},
		{"+/broker/uptime", "$SYS/broker/uptime", false},
		{"$SYS/#", "$SYS/broker/uptime", true},
	}
	for _, tt := range tests {
		if got := matchFilter(tt.filter, tt.topic); got != tt.want {
			t.Errorf("matchFilter(%q, %q) = %v; quiere %v", tt.filter, tt.topic, got, tt.want)
		}
	}
}
