package scheduler

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrickleUpFilesAreSentMarkedAndRemovedOnAck(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "trickle_up_wu_1_r0_1700000000"), []byte("<variety>credit</variety>\n<cpu>12.5</cpu>\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "unrelated"), []byte("x"), 0o644)
	msgs := readTrickleFiles(dir)
	if len(msgs) != 1 || msgs[0].ResultName != "wu_1_r0" || msgs[0].Time != 1700000000 || !strings.Contains(msgs[0].Body, "<variety>credit</variety>") {
		t.Fatalf("got %+v", msgs)
	}
	out, _ := xml.MarshalIndent(msgs[0], "", " ")
	for _, want := range []string{"<msg_from_host>", "<result_name>wu_1_r0</result_name>", "<time>1700000000</time>", "<variety>credit</variety>"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("request lacks %s:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "trickle_up_wu_1_r0_1700000000.sent")); err != nil {
		t.Error("a sent trickle must be renamed .sent")
	}
	// Resent until acknowledged, then removed.
	if again := readTrickleFiles(dir); len(again) != 1 {
		t.Error("an unacknowledged trickle is sent again")
	}
	removeSentTrickles(dir)
	if len(readTrickleFiles(dir)) != 0 {
		t.Error("acknowledged trickles must be gone")
	}
}

func TestTrickleDownIsWrittenToTheSlotWithoutHeaderTags(t *testing.T) {
	var reply Reply
	if err := xml.Unmarshal([]byte("<scheduler_reply><trickle_down>\n<result_name>r</result_name>\n<time>42</time>\n<msg>hello</msg>\n</trickle_down><message_ack/></scheduler_reply>"), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.TrickleDowns) != 1 || reply.TrickleDowns[0].ResultName != "r" || reply.MessageAck == nil {
		t.Fatalf("parsed wrongly: %+v", reply)
	}
	slot := t.TempDir()
	if err := writeTrickleDown(slot, reply.TrickleDowns[0]); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(slot, "trickle_down_42"))
	if err != nil || strings.TrimSpace(string(b)) != "<msg>hello</msg>" {
		t.Errorf("file = %q %v", b, err)
	}
}
