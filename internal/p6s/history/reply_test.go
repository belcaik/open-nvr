package history

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReplyGolden(t *testing.T) {
	tests := []struct {
		name    string
		id      uint16
		payload string
		want    Reply
	}{
		{"HistoryStreamCreate", 125, `<Command ID="125"><HistoryStreamCreateReply CmdReply="0" MediaFlag="2003281556" Version="2"></HistoryStreamCreateReply></Command>`, Reply{CmdReply: "0", MediaFlag: "2003281556", Version: "2"}},
		{"HistoryStreamDestroy", 127, `<Command ID="127"><HistoryStreamDestroyReply CmdReply="1" Version="2"></HistoryStreamDestroyReply></Command>`, Reply{CmdReply: "1", Version: "2"}},
		{"HistoryStreamPos", 129, `<Command ID="129"><HistoryStreamPosReply CmdReply="0"></HistoryStreamPosReply></Command>`, Reply{CmdReply: "0"}},
		{"HistoryStreamCtl", 131, `<Command ID="131"><HistoryStreamCtlReply CmdReply="0"></HistoryStreamCtlReply></Command>`, Reply{CmdReply: "0"}},
		{"FileDownloadCreate", 57, `<Command ID="57"><FileDownloadCreateReply CmdReply="0" MediaFlag="88" Version="1"></FileDownloadCreateReply></Command>`, Reply{CmdReply: "0", MediaFlag: "88", Version: "1"}},
		{"FileDownloadRelease", 60, `<Command ID="60"><FileDownloadReleaseReply CmdReply="0" Version="1"></FileDownloadReleaseReply></Command>`, Reply{CmdReply: "0", Version: "1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseReply(Message{ID: test.id, Payload: []byte(test.payload)})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("reply = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestControlAndSeekRepliesIgnoreCapturedGarbageAttributes(t *testing.T) {
	tests := []struct {
		id      uint16
		payload string
	}{
		{129, `<Command ID="129"><HistoryStreamPosReply CmdReply="0" Version="1482436176"></HistoryStreamPosReply></Command>`},
		{131, `<Command ID="131"><HistoryStreamCtlReply Operation="40581976" CmdReply="0" Version="28758304"></HistoryStreamCtlReply></Command>`},
	}
	for _, test := range tests {
		reply, err := ParseReply(Message{ID: test.id, Payload: []byte(test.payload)})
		if err != nil {
			t.Fatalf("ID %d: %v", test.id, err)
		}
		if reply.CmdReply != "0" || reply.MediaFlag != "" || reply.Version != "" {
			t.Fatalf("ID %d: reply = %#v", test.id, reply)
		}
	}
}

func TestEveryReplyRequiresCmdReply(t *testing.T) {
	for id, element := range map[uint16]string{
		125: "HistoryStreamCreateReply", 127: "HistoryStreamDestroyReply",
		129: "HistoryStreamPosReply", 131: "HistoryStreamCtlReply",
		57: "FileDownloadCreateReply", 60: "FileDownloadReleaseReply",
	} {
		t.Run(element, func(t *testing.T) {
			payload := fmt.Sprintf(`<Command ID="%d"><%s MediaFlag="1" Version="1"></%s></Command>`, id, element, element)
			if _, err := ParseReply(Message{ID: id, Payload: []byte(payload)}); err == nil {
				t.Fatal("expected missing CmdReply error")
			}
		})
	}
}

func TestCreateRepliesRequireCapturedFields(t *testing.T) {
	for _, test := range []struct {
		id         uint16
		element    string
		attributes []string
	}{
		{125, "HistoryStreamCreateReply", []string{`CmdReply="0" Version="1"`}},
		{57, "FileDownloadCreateReply", []string{`CmdReply="0" Version="1"`, `CmdReply="0" MediaFlag="1"`}},
	} {
		for _, attributes := range test.attributes {
			payload := fmt.Sprintf(`<Command ID="%d"><%s %s></%s></Command>`, test.id, test.element, attributes, test.element)
			if _, err := ParseReply(Message{ID: test.id, Payload: []byte(payload)}); err == nil {
				t.Fatalf("ID %d attributes %q: expected validation error", test.id, attributes)
			}
		}
	}
}

func TestHistoryRepliesDoNotRequireSpuriousVersion(t *testing.T) {
	for _, test := range []struct {
		id      uint16
		element string
	}{{125, "HistoryStreamCreateReply"}, {127, "HistoryStreamDestroyReply"}} {
		attributes := `CmdReply="0"`
		if test.id == 125 {
			attributes += ` MediaFlag="516516436"`
		}
		payload := fmt.Sprintf(`<Command ID="%d"><%s %s></%s></Command>`, test.id, test.element, attributes, test.element)
		if _, err := ParseReply(Message{ID: test.id, Payload: []byte(payload)}); err != nil {
			t.Fatalf("ID %d: %v", test.id, err)
		}
	}
}

func TestRejectedHistoryCreateIsCorrelatedByIDAndCmdReply(t *testing.T) {
	payload := `<Command ID="125"><HistoryStreamCreateReply CmdReply="2" Version="28758304"></HistoryStreamCreateReply></Command>`
	reply, err := ParseReply(Message{ID: 125, Payload: []byte(payload)})
	if err != nil {
		t.Fatal(err)
	}
	if reply.CmdReply != "2" || reply.MediaFlag != "" {
		t.Fatalf("reply = %#v", reply)
	}
}

func TestDownloadReleaseReplyStillRequiresVersion(t *testing.T) {
	payload := `<Command ID="60"><FileDownloadReleaseReply CmdReply="0"></FileDownloadReleaseReply></Command>`
	if _, err := ParseReply(Message{ID: 60, Payload: []byte(payload)}); err == nil {
		t.Fatal("expected missing Version error")
	}
}

func TestParseReplyRejectsInvalidEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{"Malformed", Message{ID: 127, Payload: []byte(`<Command`)}},
		{"WrongRoot", Message{ID: 127, Payload: []byte(`<Reply ID="127"><HistoryStreamDestroyReply CmdReply="0"></HistoryStreamDestroyReply></Reply>`)}},
		{"MismatchedID", Message{ID: 127, Payload: []byte(`<Command ID="129"><HistoryStreamDestroyReply CmdReply="0"></HistoryStreamDestroyReply></Command>`)}},
		{"WrongElement", Message{ID: 127, Payload: []byte(`<Command ID="127"><HistoryStreamPosReply CmdReply="0"></HistoryStreamPosReply></Command>`)}},
		{"RequestID", Message{ID: 126, Payload: []byte(`<Command ID="126"><HistoryStreamDestroy MediaFlag="1"></HistoryStreamDestroy></Command>`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseReply(test.message); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}
