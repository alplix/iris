package scheduler

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Trickle messages: small messages an application sends to its project while
// it runs (trickle-up: intermediate results, credit for long tasks) and the
// project sends back to a running task (trickle-down). The worker moves an
// application's trickle_up.xml into the project folder as
// trickle_up_<result>_<time>; this side sends those with the next scheduler
// request, marks them ".sent", and deletes them once the server acknowledges
// with <message_ack/>. All exactly as client/cs_trickle.cpp does.

// MsgFromHostXML is one trickle-up in a request.
type MsgFromHostXML struct {
	XMLName    xml.Name `xml:"msg_from_host"`
	ResultName string   `xml:"result_name"`
	Time       int64    `xml:"time"`
	Body       string   `xml:",innerxml"`
}

// TrickleDownXML is one trickle-down in a reply: everything except the two
// header tags is the message body handed to the task.
type TrickleDownXML struct {
	ResultName string `xml:"result_name"`
	Time       int64  `xml:"time"`
	Body       string `xml:",innerxml"`
}

const trickleUpPrefix = "trickle_up_"

// readTrickleFiles turns a project folder's pending trickle-ups into request
// entries and marks the files as sent.
func readTrickleFiles(projDir string) []MsgFromHostXML {
	if projDir == "" {
		return nil
	}
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return nil
	}
	var out []MsgFromHostXML
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, trickleUpPrefix) {
			continue
		}
		base := strings.TrimSuffix(name, ".sent")
		rest := strings.TrimPrefix(base, trickleUpPrefix)
		i := strings.LastIndex(rest, "_")
		if i <= 0 {
			continue
		}
		t, err := strconv.ParseInt(rest[i+1:], 10, 64)
		if err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join(projDir, name))
		if err != nil {
			continue
		}
		out = append(out, MsgFromHostXML{ResultName: rest[:i], Time: t, Body: "\n" + strings.TrimRight(string(body), "\n") + "\n"})
		if !strings.HasSuffix(name, ".sent") {
			os.Rename(filepath.Join(projDir, name), filepath.Join(projDir, name+".sent"))
		}
	}
	return out
}

// removeSentTrickles deletes the trickle-ups the server acknowledged (only the
// ".sent" ones; others arrived while the request was on its way).
func removeSentTrickles(projDir string) {
	if projDir == "" {
		return
	}
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, trickleUpPrefix) && strings.HasSuffix(n, ".sent") {
			os.Remove(filepath.Join(projDir, n))
		}
	}
}

// writeTrickleDown puts a trickle-down into the task's slot as
// trickle_down_<time>; the worker tells the running application.
func writeTrickleDown(slot string, td TrickleDownXML) error {
	if slot == "" {
		return fmt.Errorf("task has no slot")
	}
	body := strings.TrimLeft(td.Body, "\n")
	// The body still holds the header tags as innerxml; strip them.
	for _, tag := range []string{"result_name", "time"} {
		if i := strings.Index(body, "<"+tag+">"); i >= 0 {
			if j := strings.Index(body[i:], "</"+tag+">"); j >= 0 {
				body = body[:i] + body[i+j+len(tag)+3:]
			}
		}
	}
	body = strings.TrimLeft(body, "\n")
	return os.WriteFile(filepath.Join(slot, fmt.Sprintf("trickle_down_%d", td.Time)), []byte(body), 0o644)
}
