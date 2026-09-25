package worker

import (
	"math"
	"testing"
)

func TestChannelFollowsTheReferenceProtocol(t *testing.T) {
	s := newShmemOver(make([]byte, shmSize), "", nil)
	if _, ok := s.get(chAppStatus); ok {
		t.Fatal("a fresh channel is empty")
	}
	if !s.send(chHeartbeat, "<heartbeat/>") {
		t.Fatal("first send must succeed")
	}
	if s.send(chHeartbeat, "<heartbeat/>") {
		t.Error("send must refuse while an unread message is pending (send_msg)")
	}
	s.sendOverwrite(chHeartbeat, "<x/>")
	if m, ok := s.get(chHeartbeat); !ok || m != "<x/>" {
		t.Errorf("overwrite then get = %q %v", m, ok)
	}
	if s.mem[chHeartbeat*shmChannelSize] != 0 {
		t.Error("get must clear the pending flag")
	}
	// Channels do not bleed into each other.
	s.send(chProcessControlRequest, "<suspend/>")
	if _, ok := s.get(chHeartbeat); ok {
		t.Error("a message in one channel must not appear in another")
	}
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'a'
	}
	s.sendOverwrite(chTrickleUp, string(long))
	if m, _ := s.get(chTrickleUp); len(m) != shmChannelSize-2 {
		t.Errorf("an over-long message must be cut to the channel, got %d", len(m))
	}
}

func TestParseAppStatusFromTheAPIsMessage(t *testing.T) {
	st, ok := parseAppStatus("<current_cpu_time>1.234500e+02</current_cpu_time>\n<checkpoint_cpu_time>1.0e+02</checkpoint_cpu_time>\n<want_network>0</want_network>\n<fraction_done>2.500000e-01</fraction_done>\n")
	if !ok || math.Abs(st.cpuTime-123.45) > 1e-9 || math.Abs(st.fraction-0.25) > 1e-9 || !st.haveFraction || st.checkpointAt != 100 {
		t.Errorf("got %+v %v", st, ok)
	}
	if st, ok := parseAppStatus("<current_cpu_time>5</current_cpu_time>"); !ok || st.haveFraction {
		t.Errorf("a status without fraction_done must say so: %+v", st)
	}
	if _, ok := parseAppStatus("garbage"); ok {
		t.Error("garbage is not a status")
	}
}

func TestCreateShmemGivesAWritableSegment(t *testing.T) {
	slot := t.TempDir()
	s, err := createShmem(slot)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.initTag == "" || len(s.mem) != shmSize {
		t.Fatalf("segment wrong: tag %q size %d", s.initTag, len(s.mem))
	}
	if !s.send(chAppStatus, "<current_cpu_time>1</current_cpu_time>") {
		t.Error("cannot write to the segment")
	}
}
