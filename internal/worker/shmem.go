package worker

import (
	"strconv"
	"strings"
	"sync"
)

// The BOINC API's shared-memory channel between the client and a running
// application (lib/app_ipc.h): eight message channels of 1024 bytes. The first
// byte of a channel is non-zero while it holds an unread message, set by the
// sender and cleared by the receiver; the other 1023 bytes are the text.
const (
	shmChannelSize = 1024
	shmChannels    = 8
	shmSize        = shmChannelSize * shmChannels

	chProcessControlRequest = 0 // client -> app: <suspend/> <resume/> <quit/> <abort/>
	chProcessControlReply   = 1
	chGraphicsRequest       = 2
	chGraphicsReply         = 3
	chHeartbeat             = 4 // client -> app
	chAppStatus             = 5 // app -> client: cpu time, fraction done
	chTrickleUp             = 6
	chTrickleDown           = 7
)

// shmem is one task's shared-memory segment.
type shmem struct {
	mu    sync.Mutex
	mem   []byte
	close func()
	// name is the value for init_data.xml so the application can find the
	// segment: a comm_obj_name on Windows, or a shm_key of -1 (a memory-mapped
	// file in the slot directory) everywhere else.
	initTag string
}

// newShmemOver wraps memory that is shared with the application.
func newShmemOver(mem []byte, initTag string, closeFn func()) *shmem {
	return &shmem{mem: mem, initTag: initTag, close: closeFn}
}

func (s *shmem) channel(i int) []byte { return s.mem[i*shmChannelSize : (i+1)*shmChannelSize] }

// send writes msg into the channel unless it still holds an unread message
// (the reference client's send_msg); it reports whether it was written.
func (s *shmem) send(i int, msg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.channel(i)
	if ch[0] != 0 {
		return false
	}
	s.write(ch, msg)
	return true
}

// sendOverwrite writes msg even over an unread one (send_msg_overwrite).
func (s *shmem) sendOverwrite(i int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(s.channel(i), msg)
}

func (s *shmem) write(ch []byte, msg string) {
	if len(msg) > shmChannelSize-2 {
		msg = msg[:shmChannelSize-2]
	}
	n := copy(ch[1:], msg)
	ch[1+n] = 0
	ch[0] = 1
}

// get returns and clears the channel's message, if there is one.
func (s *shmem) get(i int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.channel(i)
	if ch[0] == 0 {
		return "", false
	}
	end := 1
	for end < len(ch) && ch[end] != 0 {
		end++
	}
	msg := string(ch[1:end])
	ch[0] = 0
	return msg, true
}

// reset clears every channel (before the application starts).
func (s *shmem) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.mem {
		s.mem[i] = 0
	}
}

// Close releases the segment.
func (s *shmem) Close() {
	if s != nil && s.close != nil {
		s.close()
		s.close = nil
	}
}

// appStatus is what the application reports through the app_status channel.
type appStatus struct {
	cpuTime      float64
	checkpointAt float64
	fraction     float64
	haveFraction bool
}

// parseAppStatus reads the tags the API writes (api/boinc_api.cpp,
// update_app_progress).
func parseAppStatus(msg string) (appStatus, bool) {
	var st appStatus
	got := false
	tag := func(name string) (float64, bool) {
		open := "<" + name + ">"
		i := strings.Index(msg, open)
		if i < 0 {
			return 0, false
		}
		rest := msg[i+len(open):]
		j := strings.Index(rest, "</"+name+">")
		if j < 0 {
			return 0, false
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest[:j]), 64)
		return v, err == nil
	}
	if v, ok := tag("current_cpu_time"); ok {
		st.cpuTime, got = v, true
	}
	if v, ok := tag("checkpoint_cpu_time"); ok {
		st.checkpointAt = v
	}
	if v, ok := tag("fraction_done"); ok {
		st.fraction, st.haveFraction, got = v, true, true
	}
	return st, got
}
